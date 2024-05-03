//
//  MIT License
//
//  (C) Copyright 2019-2024 Hewlett Packard Enterprise Development LP
//
//  Permission is hereby granted, free of charge, to any person obtaining a
//  copy of this software and associated documentation files (the "Software"),
//  to deal in the Software without restriction, including without limitation
//  the rights to use, copy, modify, merge, publish, distribute, sublicense,
//  and/or sell copies of the Software, and to permit persons to whom the
//  Software is furnished to do so, subject to the following conditions:
//
//  The above copyright notice and this permission notice shall be included
//  in all copies or substantial portions of the Software.
//
//  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
//  IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
//  FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
//  THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
//  OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
//  ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
//  OTHER DEALINGS IN THE SOFTWARE.
//

// This file contains the code needed to find node information

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
)

type NodeService interface {
	getRedfishEndpoints() ([]redfishEndpoint, error)
	getStateComponents() ([]stateComponent, error)
	getCurrentNodesFromHSM() (nodes []nodeConsoleInfo)
	updateNodeCounts(numMtnNodes, numRvrNodes int)
}

// Implements NodeService
type NodeManager struct {
	k8Service K8Service
}

// Inject dependencies
func NewNodeManager(k8Service K8Service) NodeService {
	return &NodeManager{k8Service: k8Service}
}

// Struct to hold all node level information needed to form a console connection
// NOTE: this is the basic unit of information required for each node
// NOTE: expected values for 'Class' are:
//
//	Mountain - Cray hardware in full liquid cooled rack (ssh via key)
//	Hill - Cray hardware in freestanding rack (ssh via key)
//	River - Other brand hardware in freestanding rack (ipmi via user/password)
//	Paradise - Cray xd224 - foxconn bmc (ssh via user/password)
type nodeConsoleInfo struct {
	NodeName string // node xname
	BmcName  string // bmc xname
	BmcFqdn  string // full name of bmc
	Class    string // river/mtn class
	NID      int    // NID of the node
	Role     string // role of the node
}

// Function to determine if a node is Mountain hardware
func (node nodeConsoleInfo) isMountain() bool {
	return node.Class == "Mountain" || node.Class == "Hill"
}

// Function to determine if a node is River hardware
func (node nodeConsoleInfo) isRiver() bool {
	return node.Class == "River"
}

// Function to determine if a node is Paradise hardware
func (node nodeConsoleInfo) isParadise() bool {
	return node.Class == "Paradise"
}

// Provide a function to convert struct to string
func (nc nodeConsoleInfo) String() string {
	return fmt.Sprintf("NodeName:%s, BmcName:%s, BmcFqdn:%s, Class:%s, NID:%d, Role:%s",
		nc.NodeName, nc.BmcName, nc.BmcFqdn, nc.Class, nc.NID, nc.Role)
}

// Struct to hold hsm redfish endpoint information
type redfishEndpoint struct {
	ID       string
	Type     string
	FQDN     string
	User     string
	Password string
}

// Provide a function to convert struct to string
func (re redfishEndpoint) String() string {
	return fmt.Sprintf("ID:%s, Type:%s, FQDN:%s, User:%s, Password:REDACTED", re.ID, re.Type, re.FQDN, re.User)
}

// Struct to hold hsm state component information
type stateComponent struct {
	ID    string
	Type  string
	Class string `json:",omitempty"`
	NID   int    `json:",omitempty"` // NOTE: NID value only valid if Role="Compute"
	Role  string `json:",omitempty"`
}

// Provide a function to convert struct to string
func (sc stateComponent) String() string {
	return fmt.Sprintf("ID:%s, Type:%s, Class:%s, NID:%d, Role:%s", sc.ID, sc.Type, sc.Class, sc.NID, sc.Role)
}

// Query hsm for redfish endpoint information
func (NodeManager) getRedfishEndpoints() ([]redfishEndpoint, error) {
	type response struct {
		RedfishEndpoints []redfishEndpoint
	}

	// Query hsm to get the redfish endpoints
	URL := "http://cray-smd/hsm/v2/Inventory/RedfishEndpoints"
	data, _, err := getURL(URL, nil)
	if err != nil {
		log.Printf("Unable to get redfish endpoints from hsm:%s", err)
		return nil, err
	}

	// decode the response
	rp := response{}
	err = json.Unmarshal(data, &rp)
	if err != nil {
		log.Printf("Error unmarshalling data: %s", err)
		return nil, err
	}

	return rp.RedfishEndpoints, nil
}

// Query hsm for state component information
func (NodeManager) getStateComponents() ([]stateComponent, error) {
	// get the component states from hsm - includes river/mountain information
	type response struct {
		Components []stateComponent
	}

	// get the state components from hsm
	URL := "http://cray-smd/hsm/v2/State/Components"
	data, _, err := getURL(URL, nil)
	if err != nil {
		log.Printf("Unable to get state component information from hsm:%s", err)
		return nil, err
	}

	// decode the response
	rp := response{}
	err = json.Unmarshal(data, &rp)
	if err != nil {
		// handle error
		log.Printf("Error unmarshalling data: %s", err)
		return nil, nil
	}

	return rp.Components, nil
}

// Query hsm for Paradise (xd224) nodes
func (NodeManager) getParadiseNodes() (map[string]struct{}, error) {
	// Paradise nodes are identified by having the manufacturer as 'Foxconn' and
	// the model as either 'HPE Cray Supercomputing XD224' or '1A62WCB00-600-G'.
	// There are a limited number of units that were sent to the field with the
	// incorrect model '1A62WCB00-600-G' so we must support that.

	// Structs to unmarshal the inventory data we care about
	type HsmNodeFRUInfo struct {
		Model        string
		Manufacturer string
		PartNumber   string
		SerialNumber string
	}
	type HsmPopulatedFRU struct {
		Type        string
		Subtype     string
		NodeFRUInfo HsmNodeFRUInfo
	}
	type HsmHardwareInventoryItem struct {
		ID           string
		Type         string
		PopulatedFRU HsmPopulatedFRU
	}

	// Query hsm to get the Paradise nodes
	// NOTE: this only pulls the Foxconn BMCs from the inventory so there is a bit of
	//  server side filtering going on
	URL := "http://cray-smd/hsm/v2/Inventory/Hardware?Manufacturer=Foxconn&Type=Node"
	data, _, err := getURL(URL, nil)
	if err != nil {
		log.Printf("Unable to get hardware inventory from hsm:%s", err)
		return nil, err
	}

	// decode the response
	rp := []HsmHardwareInventoryItem{}
	err = json.Unmarshal(data, &rp)
	if err != nil {
		log.Printf("Error unmarshalling data: %s", err)
		return nil, err
	}

	// create a set of the Paradise items
	nodes := map[string]struct{}{}
	for _, node := range rp {
		if node.PopulatedFRU.NodeFRUInfo.Model == "HPE Cray Supercomputing XD224" ||
			node.PopulatedFRU.NodeFRUInfo.Model == "1A62WCB00-600-G" {
			nodes[node.ID] = struct{}{}
		}
	}

	return nodes, nil
}

func (nm NodeManager) getCurrentNodesFromHSM() (nodes []nodeConsoleInfo) {
	// Get the BMC IP addresses and user, and password for individual nodes.
	// conman is only set up for River nodes.
	log.Printf("Starting to get current nodes on the system")

	rfEndpoints, err := nm.getRedfishEndpoints()
	if err != nil {
		log.Printf("Unable to build configuration file - error fetching redfish endpoints: %s", err)
		return nil
	}

	// get the state information to find mountain/river designation
	stComps, err := nm.getStateComponents()
	if err != nil {
		log.Printf("Unable to build configuration file - error fetching state components: %s", err)
		return nil
	}

	// get the paradise nodes
	// NOTE: this returns a pseudo-set to speed up lookups
	paradiseNodes, err := nm.getParadiseNodes()
	if err != nil {
		// log the error but don't die - most systems will not have Paradise nodes anyway
		log.Printf("Unable to identify if there are any Paradise nodes on the system. %s", err)
	}

	// create a lookup map for the redfish information
	rfMap := make(map[string]redfishEndpoint)
	for _, rf := range rfEndpoints {
		rfMap[rf.ID] = rf
	}

	// create river and mountain node information
	nodes = nil
	for _, sc := range stComps {
		if sc.Type == "Node" {
			// create a new entry for this node - take initial vals from state component info
			newNode := nodeConsoleInfo{NodeName: sc.ID, Class: sc.Class, NID: sc.NID, Role: sc.Role}

			// If this is a paradise node, switch the class name
			if _, isParadise := paradiseNodes[sc.ID]; isParadise {
				newNode.Class = "Paradise"
			}

			// pull information about the node BMC from the redfish information
			bmcName := sc.ID[0:strings.LastIndex(sc.ID, "n")]
			//log.Printf("Parsing node info. Node:%s, bmc:%s", sc.ID, bmcName)
			if rf, ok := rfMap[bmcName]; ok {
				//log.Print("  Found redfish endpoint info")
				// found the bmc in the redfish information
				newNode.BmcName = bmcName
				newNode.BmcFqdn = rf.FQDN

				// add to the list of nodes
				nodes = append(nodes, newNode)

			} else {
				log.Printf("Node with no BMC present: %s, bmcName:%s", sc.ID, bmcName)
			}
		}
	}

	return nodes
}

// update settings based on the current number of nodes in the system
func (nm NodeManager) updateNodeCounts(numMtnNodes, numRvrNodes int) {
	// update the number of pods based on max numbers
	// NOTE: at this point we will require one more than absolutely required both
	//  to handle the edge case of exactly matching a multiple of the max per
	//  pod as well as adding a little resiliency
	log.Printf("Mountain current: %d, max per node: %d", numMtnNodes, maxMtnNodesPerPod)
	log.Printf("River    current: %d, max per node: %d", numRvrNodes, maxRvrNodesPerPod)

	// bail if there hasn't been anything reported yet - don't want to change
	// replica count when hsm hasn't been populated (or contacted) yet
	if numMtnNodes+numRvrNodes == 0 {
		log.Printf("No nodes found, skipping count update")
		return
	}

	// lets be extra paranoid about divide by zero issues...
	mm := math.Max(float64(maxMtnNodesPerPod), 1)
	mr := math.Max(float64(maxRvrNodesPerPod), 1)

	// calculate number of pods needed for mountain and river nodes, choose max
	numMtnReq := int(math.Ceil(float64(numMtnNodes)/mm) + 1)
	numRvrReq := int(math.Ceil(float64(numRvrNodes)/mr) + 1)
	newNumPods := numMtnReq
	if numRvrReq > newNumPods {
		newNumPods = numRvrReq
	}

	// update the number of nodes / pod based on number of pods
	nm.k8Service.updateReplicaCount(newNumPods)

	// update the number of mtn + river consoles to watch per pod
	// NOTE: adding a little slop to how many each pod wants
	// needed for worst case where a replica can acquire more nodes
	// however, the only available nodes are themselves. Adding the replica counts
	// will allow room to avoid orphaned mtn or rvr nodes.
	newMtn := int(math.Ceil(float64(numMtnNodes)/float64(newNumPods)) + 1)
	newRvr := int(math.Ceil(float64(numRvrNodes)/float64(newNumPods)) + 1)
	currNodeReplicas, err := nm.k8Service.getReplicaCount()
	if err != nil {
		newMtn += currNodeReplicas
		newRvr += currNodeReplicas
		log.Printf("Adding replica padding per pod- Mtn: %d, Rvr: %d", newMtn, newRvr)
		nm.k8Service.updateNodesPerPod(newMtn, newRvr)
	} else {
		log.Printf("New number of nodes per pod- Mtn: %d, Rvr: %d", newMtn, newRvr)
		// push new numbers where they need to go
		if newRvr != numRvrNodesPerPod || newMtn != numMtnNodesPerPod {
			// something changed so we need to update
			nm.k8Service.updateNodesPerPod(newMtn, newRvr)
		}
	}
}
