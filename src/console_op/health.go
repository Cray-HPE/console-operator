//
//  MIT License
//
//  (C) Copyright 2021-2024 Hewlett Packard Enterprise Development LP
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
	"fmt"
	"log"
	"net/http"
)

type HealthService interface {
	doLiveness(w http.ResponseWriter, r *http.Request)
	doHealth(w http.ResponseWriter, r *http.Request)
	doReadiness(w http.ResponseWriter, r *http.Request)
	getCurrentHealth() HealthResponse
}

// Implements HealthService
type HealthManager struct {
	dataService DataService
}

// Constructor injection for dependencies
func NewHealthManager(ds DataService) HealthService {
	return &HealthManager{dataService: ds}
}

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
func (hm HealthManager) doHealth(w http.ResponseWriter, r *http.Request) {
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
	stats := hm.getCurrentHealth()

	// log the query
	log.Printf("Health check: %s", stats)

	// write the output
	SendResponseJSON(w, http.StatusOK, stats)
	return
}

// Fill out the current status of a HealthResponse object
func (HealthManager) getCurrentHealth() HealthResponse {
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
func (HealthManager) doLiveness(w http.ResponseWriter, r *http.Request) {
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
}

// Basic readiness probe
func (HealthManager) doReadiness(w http.ResponseWriter, r *http.Request) {
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
}
