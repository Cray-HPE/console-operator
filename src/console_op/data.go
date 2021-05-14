/*
 * MIT License
 *
 * (C) Copyright [2021] Hewlett Packard Enterprise Development LP
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
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
 * THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
 * OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
 * ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
 * OTHER DEALINGS IN THE SOFTWARE.
 */

// This file contains the code needed to interact with the console-data
//  service.

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

// Variable to hold address of console-data service
var dataAddrBase string = "http://cray-console-data/v1"

// function to interact with console-data api to add new nodes to the db
func dataAddNodes(newNodes []nodeConsoleInfo) bool {
	// return if there was a successful response from console-data
	retVal := false

	// Just log a summary
	log.Printf("Sending %d nodes to console-data", len(newNodes))

	// NOTE: data is just a simple array of nodeConsoleInfo structs - no packaging
	data, err := json.Marshal(newNodes)
	if err != nil {
		log.Printf("Error marshalling data for add nodes:%s", err)
		return retVal
	}

	// use 'PUT' to get into data service
	URL := dataAddrBase + "/inventory"
	rd, rc, err := putURL(URL, data, nil)
	if err != nil {
		log.Printf("Error adding new data to console-data inventory: %s", err)
		return retVal
	}

	// log if call succeeded (anything less than http 400 is success)
	retVal = rc < 400

	// decode the response
	type response struct {
		message string
	}
	rp := response{}
	err = json.Unmarshal(rd, &rp)
	if err != nil {
		// handle error
		log.Printf("Error unmarshalling data: %s, bytesArray:%s", err, rd)
	} else {
		log.Printf("Console-data return message: %s", rp.message)
	}
	return retVal
}

// function to interact with console-data api to remove existing nodes from the db
func dataRemoveNodes(removedNodes []nodeConsoleInfo) {
	// NOTE: data is just a simple array of nodeConsoleInfo structs - no packaging
	data, err := json.Marshal(removedNodes)
	if err != nil {
		log.Printf("Error marshalling data for remove nodes:%s", err)
		return
	}

	// dump input to log
	log.Printf("Nodes removing from console-data:")
	for _, ni := range removedNodes {
		log.Printf("  Node: %s", ni.NodeName)
	}

	// use 'DELETE' to get into data service
	URL := dataAddrBase + "/inventory"
	rd, rc, err := deleteURL(URL, data, nil)
	if err != nil {
		log.Printf("Unable to remove elements from console-data: %s", err)
		return
	}

	if rd != nil {
		// decode the response
		type response struct {
			message string
		}
		rp := response{}
		err = json.Unmarshal(rd, &rp)
		if err != nil {
			// handle error
			// TODO - better error handling?  Do we need a retry so if something fails
			//  it won't get out of sync??
			log.Printf("Error unmarshalling data: %s", err)
		} else {
			log.Printf("Console-data return message: %s", rp.message)
		}
	} else {
		log.Printf("Console-data had no return data, response code: %d", rc)
	}

}

// trigger a clearing of nodes from a stale pod
func checkHeartbeats() {
	for {
		log.Printf("Checking for stale heartbeats")
		// format the url for the clear API
		url := fmt.Sprintf("%s/consolepod/%d/clear", dataAddrBase, heartbeatStaleMinutes)

		// call the console-data api
		_, _, err := deleteURL(url, nil, nil)
		if err != nil {
			log.Printf("Error calling console-data clear stale heartbeats:%s", err)
		}

		// wait for the next interval
		time.Sleep(time.Duration(heartbeatCheckPeriodSec) * time.Second)

	}
}

// GetNodePodResponse - used to report service health stats
type GetNodePodResponse struct {
	PodName string `json:"podname"`
}

// GetNodeData - input data for call to getNodeData
type GetNodeData struct {
	XName string `json:"xname"`
}

// BaseResponse - error message for a bad response
type BaseResponse struct {
	Msg string `json:"message"`
}

// Get which pod a particular console is connected to
func doGetNodePod(w http.ResponseWriter, r *http.Request) {
	// NOTE: this is provided as a quick check of the internal status for
	//  administrators to aid in determining the health of this service.

	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
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

	var inData GetNodeData
	err = json.Unmarshal(reqBody, &inData)
	if err != nil {
		log.Printf("There was an error while decoding the json data: %s\n", err)
		var body = BaseResponse{
			Msg: fmt.Sprintf("There was an error while decoding the json data: %s", err),
		}
		SendResponseJSON(w, http.StatusBadRequest, body)
		return
	}

	// get the correct pod from the console-data service
	podName, err := getNodePodForXname(inData.XName)
	if err != nil {
		log.Printf("Error getting console node pod from console-data: %s", err)
		var body = BaseResponse{
			Msg: fmt.Sprintf("There was an error querying console-data service: %s", err),
		}
		SendResponseJSON(w, http.StatusInternalServerError, body)
		return
	}

	// package and return the value
	var res GetNodePodResponse
	res.PodName = podName
	SendResponseJSON(w, http.StatusOK, res)
	return
}

// query the console-data service for the correct pod
func getNodePodForXname(xname string) (string, error) {
	// now we have the name the user is looking for, put the request to console-data
	url := fmt.Sprintf("%s/consolepod/%s", dataAddrBase, xname)
	rd, _, err := getURL(url, nil)
	if err != nil {
		log.Printf("Error getting console node pod from console-data: %s", err)
		return "", err
	}

	// pull the data from the return package
	type RetNodeConsoleInfo struct {
		NodeName        string `json:"nodename"`        // node xname
		BmcName         string `json:"bmcname"`         // bmc xname
		BmcFqdn         string `json:"bmcfqdn"`         // full name of bmc
		Class           string `json:"class"`           // river/mtn class
		NID             int    `json:"nid"`             // NID of the node
		Role            string `json:"role"`            // role of the node
		NodeConsoleName string `json:"nodeconsolename"` // the pod console
	}

	var nd RetNodeConsoleInfo
	err = json.Unmarshal(rd, &nd)
	if err != nil {
		log.Printf("Error unmashalling data from console-data: %s", err)
		return "", err
	}

	// return the result
	return fmt.Sprintf("cray-console-node-%s", nd.NodeConsoleName), nil
}
