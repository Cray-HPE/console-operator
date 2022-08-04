/*
 * Copyright 2019-2021 Hewlett Packard Enterprise Development LP
 *
 * Permission is hereby granted, free of charge, to any person obtaining a
 * copy of this software and associated documentation files (the "Software"),
 * to deal in the Software without restriction, including without limitation
 * the rights to use, copy, modify, merge, publish, distribute, sublicense,
 * and/or sell copies of the Software, and to permit persons to whom the
 * Software is furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included
 * in all copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.  IN NO EVENT SHALL
 * THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
 * OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
 * ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
 * OTHER DEALINGS IN THE SOFTWARE.
 *
 * (MIT License)
 */

// This file contains the code needed to find node information

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
)

// Struct to hold all node level information needed to form a console connection
// NOTE: this is the basic unit of information required for each node
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
func getRedfishEndpoints() ([]redfishEndpoint, error) {
	type response struct {
		RedfishEndpoints []redfishEndpoint
	}

	// Query hsm to get the redfish endpoints
	URL := "http://cray-smd/hsm/v1/Inventory/RedfishEndpoints"
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
func getStateComponents() ([]stateComponent, error) {
	// get the component states from hsm - includes river/mountain information
	type response struct {
		Components []stateComponent
	}

	// get the state components from hsm
	URL := "http://cray-smd/hsm/v1/State/Components"
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

func getCurrentNodesFromHSM() (nodes []nodeConsoleInfo) {
	// Get the BMC IP addresses and user, and password for individual nodes.
	// conman is only set up for River nodes.
	log.Printf("Starting to get current nodes on the system")

	rfEndpoints, err := getRedfishEndpoints()
	if err != nil {
		log.Printf("Unable to build configuration file - error fetching redfish endpoints: %s", err)
		return nil
	}

	// get the state information to find mountain/river designation
	stComps, err := getStateComponents()
	if err != nil {
		log.Printf("Unable to build configuration file - error fetching state components: %s", err)
		return nil
	}

	// create a lookup map for the redfish information
	rfMap := make(map[string]redfishEndpoint)
	for _, rf := range rfEndpoints {
		rfMap[rf.ID] = rf
	}

	// create river and mountain node information
	nodes = nil
	var xnames []string = nil
	for _, sc := range stComps {
		if sc.Type == "Node" {
			// create a new entry for this node - take initial vals from state component info
			newNode := nodeConsoleInfo{NodeName: sc.ID, Class: sc.Class, NID: sc.NID, Role: sc.Role}

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

				// add to list of bmcs to get creds from
				//log.Printf("Added node: %s", newNode)
				xnames = append(xnames, bmcName)
			} else {
				log.Printf("Node with no BMC present: %s, bmcName:%s", sc.ID, bmcName)
			}
		}
	}

	return nodes
}

// update settings based on the current number of nodes in the system
func updateNodeCounts(numMtnNodes, numRvrNodes int) {
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
	updateReplicaCount(newNumPods)

	// update the number of mtn + river consoles to watch per pod
	// NOTE: adding a little slop to how many each pod wants just for a little
	//  wiggle room, not strictly needed
	newMtn := int(math.Ceil(float64(numMtnNodes)/float64(newNumPods)) + 1)
	newRvr := int(math.Ceil(float64(numRvrNodes)/float64(newNumPods)) + 1)
	log.Printf("New number of nodes per pod- Mtn: %d, Rvr: %d", newMtn, newRvr)

	// push new numbers where they need to go
	if newRvr != numRvrNodesPerPod || newMtn != numMtnNodesPerPod {
		// something changed so we need to update
		updateNodesPerPod(newMtn, newRvr)
	}
}
