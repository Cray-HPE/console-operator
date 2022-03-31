//
//  MIT License
//
//  (C) Copyright 2021-2022 Hewlett Packard Enterprise Development LP
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

// This file contains the health endpoint functions

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
)

// HealthResponse - used to report service health stats
type HealthResponse struct {
	NumberConsoles       string `json:"consoles"`
	HardwareUpdateSec    string `json:"hardwareupdatesec"`
	LastHardwareUpdate   string `json:"hardwareupdate"`
	NumberNodePods       string `json:"nodepods"`
	NumberRvrNodesPerPod string `json:"rvrnodesperpod"`
	NumberMtnNodesPerPod string `json:"mtnnodesperpod"`
	MaxRvrNodesPerPod    string `json:"maxrvrnodesperpod"`
	MaxMtnNodesPerPod    string `json:"maxmtnnodesperpod"`
	HeartbeatCheckSec    string `json:"heartbeatcheck"`
	HeartbeatStaleMin    string `json:"heartbeatstale"`
}

// Debugging information query
func doHealth(w http.ResponseWriter, r *http.Request) {
	// NOTE: this is provided as a quick check of the internal status for
	//  administrators to aid in determining the health of this service.

	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// get the current health status
	stats := getCurrentHealth()

	// log the query
	log.Printf("Health check: %s", stats)

	// write the output
	SendResponseJSON(w, http.StatusOK, stats)
	return
}

// Fill out the current status of a HealthResponse object
func getCurrentHealth() HealthResponse {
	var stats HealthResponse
	stats.HardwareUpdateSec = fmt.Sprintf("%d", newHardwareCheckPeriodSec)
	stats.LastHardwareUpdate = hardwareUpdateTime
	stats.NumberConsoles = fmt.Sprintf("%d", len(nodeCache))
	stats.NumberNodePods = fmt.Sprintf("%d", numNodePods)
	stats.NumberRvrNodesPerPod = fmt.Sprintf("%d", numRvrNodesPerPod)
	stats.NumberMtnNodesPerPod = fmt.Sprintf("%d", numMtnNodesPerPod)
	stats.MaxRvrNodesPerPod = fmt.Sprintf("%d", maxRvrNodesPerPod)
	stats.MaxMtnNodesPerPod = fmt.Sprintf("%d", maxMtnNodesPerPod)
	stats.HeartbeatCheckSec = fmt.Sprintf("%d", heartbeatCheckPeriodSec)
	stats.HeartbeatStaleMin = fmt.Sprintf("%d", heartbeatStaleMinutes)
	return stats
}

// Basic liveness probe
func doLiveness(w http.ResponseWriter, r *http.Request) {
	// NOTE: this is coded in accordance with kubernetes best practices
	//  for liveness/readiness checks.  This function should only be
	//  used to indicate the server is still alive and processing requests.

	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// return simple StatusOK response to indicate server is alive
	w.WriteHeader(http.StatusNoContent)
	return
}

// Basic readiness probe
func doReadiness(w http.ResponseWriter, r *http.Request) {
	// NOTE: this is coded in accordance with kubernetes best practices
	//  for liveness/readiness checks.  This function should only be
	//  used to indicate the server is still alive and processing requests.

	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// return simple StatusOK response to indicate server is alive
	w.WriteHeader(http.StatusNoContent)
	return
}

///////////////////////////////////////////////////////////////////////////////
///////////////////////////////////////////////////////////////////////////////
// Added some debug endpoints below for useful testing / probing of live
//  systems.  They are not documented, but are present.
///////////////////////////////////////////////////////////////////////////////
///////////////////////////////////////////////////////////////////////////////

// MaxNodeData - Simple struct to return error information
type MaxNodeData struct {
	MaxRvrNodes int `json:"maxRvr"` // max number of river nodes per pod
	MaxMtnNodes int `json:"maxMtn"` // max number of mountain nodes per pod
}

// small helper function to ensure correct number of nodes asked for
func pinNumNodes(numAsk, numMin, numMax int) (int, bool) {
	// ensure the input number ends in range [0,numMax]
	ok := true
	val := numAsk
	if val < numMin {
		// already have too many
		val = numMin
		ok = false
	} else if val > numMax {
		// pin at the maximum
		val = numMax
		ok = false
	}
	return val, ok
}

// Debugging information probe
func doSetMaxNodesPerPod(w http.ResponseWriter, r *http.Request) {
	// API to set the max number of nodes per pod
	log.Printf("Call to setMaxNodesPerPod...")

	// only allow 'PATCH' calls
	if r.Method != http.MethodPatch {
		w.Header().Set("Allow", "PATCH")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// read the request data - must be in json content
	reqBody, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		log.Printf("There was an error reading the request body: S%s\n", err)
		var body = BaseResponse{
			Msg: fmt.Sprintf("There was an error reading the request body: S%s", err),
		}
		SendResponseJSON(w, http.StatusBadRequest, body)
		return
	}
	contentType := r.Header.Get("Content-type")
	log.Printf("Content-Type: %s\n", contentType)
	if contentType != "application/json" {
		var body = BaseResponse{
			Msg: fmt.Sprintf("Expecting Content-Type: application/json"),
		}
		SendResponseJSON(w, http.StatusBadRequest, body)
		return
	}
	log.Printf("request data: %s\n", string(reqBody))

	var inData MaxNodeData
	err = json.Unmarshal(reqBody, &inData)
	if err != nil {
		log.Printf("There was an error while decoding the json data: %s\n", err)
		var body = BaseResponse{
			Msg: fmt.Sprintf("There was an error while decoding the json data: %s", err),
		}
		SendResponseJSON(w, http.StatusBadRequest, body)
		return
	}

	// process the results - do a sanity check on the user input
	log.Printf("Resetting max nodes based on user input: maxMtn: %d, maxRvr: %d", inData.MaxMtnNodes, inData.MaxRvrNodes)
	ok := true
	maxMtnNodesPerPod, ok = pinNumNodes(inData.MaxMtnNodes, 2, 750)
	if !ok {
		log.Printf("Error - invalid max mountain nodes per pod. Asked: %d, defaulted to: %d",
			inData.MaxMtnNodes, maxMtnNodesPerPod)
	}
	maxRvrNodesPerPod, ok = pinNumNodes(inData.MaxRvrNodes, 2, 2000)
	if !ok {
		log.Printf("Error - invalid max river nodes per pod. Asked: %d, defaulted to: %d",
			inData.MaxRvrNodes, maxRvrNodesPerPod)
	}

	// write the response
	w.WriteHeader(http.StatusOK)
	return
}

// NumNodeData - Simple struct to return error information
type NumNodeData struct {
	NumRvrNodes int `json:"numRvr"` // max number of river nodes per pod
	NunMtnNodes int `json:"numMtn"` // max number of mountain nodes per pod
}

// Get the target number of nodes per console-node pod
func doGetNumNodesPerPod(w http.ResponseWriter, r *http.Request) {
	// API to set the max number of nodes per pod
	log.Printf("Call to setMaxNodesPerPod...")

	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// Load up the return data structure
	var numNodes NumNodeData
	numNodes.NumRvrNodes = numRvrNodesPerPod
	numNodes.NunMtnNodes = numMtnNodesPerPod

	log.Printf("  Sending Rvr:%d, Mtn:%d", numRvrNodesPerPod, numMtnNodesPerPod)

	// write the response
	SendResponseJSON(w, http.StatusOK, numNodes)
	return
}

// NodePodPair - information for which console-node pod an xname is controlled by
type NodePodPair struct {
	PodID    string
	NumNodes int
}

// InfoResponse - package of debug data for export
type InfoResponse struct {
	Nodes  []NodePodPair
	Health HealthResponse
}

// Debugging information probe
func doInfo(w http.ResponseWriter, r *http.Request) {
	// NOTE: this is provided as a quick check of the internal status for
	//  administrators to aid in determining the health of this service.

	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// fill in health response portion
	var info InfoResponse
	info.Health = getCurrentHealth()

	// keep track of how many nodes are connected to each node-pod
	tally := make(map[string]int)
	for nn := range nodeCache {
		podName, err := getNodePodForXname(nn)
		if err != nil {
			tally["Unassigned"] = tally["Unassigned"] + 1
		} else {
			tally[podName] = tally[podName] + 1
		}
	}

	// package into the return response
	for k, v := range tally {
		info.Nodes = append(info.Nodes, NodePodPair{PodID: k, NumNodes: v})
	}

	// write the response
	SendResponseJSON(w, http.StatusOK, info)
	return
}

// Debugging only - clear all current data from services
func doClearData(w http.ResponseWriter, r *http.Request) {
	// This will force a clear of all cached data here as well as removing all
	// node information from console-data.  That will trigger all console-nodes
	// to drop the consoles they are watching on the next heartbeat call.  All
	// will get picked up again on the next call to state manager.
	log.Printf("Calling doClearData...")

	// only allow 'DELETE' calls
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", "DELETE")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// get the pod each node is in and remove from console-data
	var rn []nodeConsoleInfo = make([]nodeConsoleInfo, 0, len(nodeCache))
	for _, ni := range nodeCache {
		rn = append(rn, ni)
	}
	nodeCache = make(map[string]nodeConsoleInfo)
	dataRemoveNodes(rn)

	// write the response
	w.WriteHeader(http.StatusOK)
	return
}

// Debugging only - suspend querying the state manager
func doSuspend(w http.ResponseWriter, r *http.Request) {
	// only allow 'POST' calls
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// HACK - if we set the 'inShutdown' flag to true it will prevent actions
	inShutdown = true

	log.Printf("Updates suspended")
	// write the response
	w.WriteHeader(http.StatusOK)
	return
}

// Debugging only - resume querying the state manager
func doResume(w http.ResponseWriter, r *http.Request) {
	// only allow 'POST' calls
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// HACK - if we set the 'inShutdown' flag to true it will prevent actions
	inShutdown = false

	log.Printf("Updates resumed")

	// write the response
	w.WriteHeader(http.StatusOK)
	return
}
