//
//  MIT License
//
//  (C) Copyright 2021-2023 Hewlett Packard Enterprise Development LP
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

// This file contains the main elements of the application used to
// monitor console applications

package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// global var to help with local running/debugging
var debugOnly bool = false

// globals for http server
var httpListen string = ":26777"

// globals to cache current node information
var nodeCache map[string]nodeConsoleInfo = make(map[string]nodeConsoleInfo)

// Number of console-node pods to have instantiated - start with -1 to initialize
var numNodePods int = -1

// Number of target nodes per pod - initialize to -1 to prevent
// startup until console-data is populated
var numRvrNodesPerPod int = -1
var numMtnNodesPerPod int = -1

// The maximum number of river/mountain nodes per pod are
// based on testing on Shasta systems at this time.  These numbers
// may need to be adjusted with more testing time on large systems.
// These are considered to be hard maximums and pods will be scaled
// so no pod will try to connect to more than this.
var maxMtnNodesPerPod int = 750
var maxRvrNodesPerPod int = 2000

// Global var to control how often we check for hardware changes
var newHardwareCheckPeriodSec int = 30
var hardwareUpdateTime string = "Unknown"

// Global vars to control checking for stale heartbeats
var heartbeatStaleMinutes int = 3
var heartbeatCheckPeriodSec int = 15

// Global var to signal we are shutting down and prevent periodic checks from happening
var inShutdown bool = false

// Function to do a hardware update check
func doHardwareUpdate(ds DataService, ns NodeService, updateAll, redeployMtnKeys bool) (updateSuccess, keySuccess bool) {
	// return if the console-data update succeeded
	updateSuccess = true
	keySuccess = true

	// record the time of the hardware update attempt
	hardwareUpdateTime = time.Now().Format(time.RFC3339)

	// get the current endpoints from hsm
	currNodes := ns.getCurrentNodesFromHSM()

	// look for new nodes
	var newNodes []nodeConsoleInfo = nil
	var newMtnNodes []nodeConsoleInfo = nil
	for _, n := range currNodes {
		if _, found := nodeCache[n.NodeName]; !found {
			// add the item to the cached items and record as new
			nodeCache[n.NodeName] = n
			newNodes = append(newNodes, n)
			if n.isMountain() {
				newMtnNodes = append(newMtnNodes, n)
			}
			log.Printf("Found new node: %s", n.String())
		}
	}

	// if we are forcing an update of all nodes, do that here
	if updateAll {
		log.Printf("Forcing inventory update of all %d nodes", len(nodeCache))
		newNodes = make([]nodeConsoleInfo, 0, len(nodeCache))
	}

	// if we are forcing an update of mtn keys, do that here
	if redeployMtnKeys {
		log.Printf("Forcing update of all mtn console keys")
		newMtnNodes = make([]nodeConsoleInfo, 0, len(nodeCache))
	}

	// look for removed nodes
	// NOTE: yes this is n^2 performance but should not be huge numbers
	//  can make this more performant later if needed
	var removedNodes []nodeConsoleInfo = nil
	numRvrNodes := 0
	numMtnNodes := 0
	for k, v := range nodeCache {
		// generate list of all if forcing a complete update
		if updateAll {
			newNodes = append(newNodes, v)
		}

		// gather all mountain nodes if forcing full key regeneration
		if redeployMtnKeys && v.isMountain() {
			newMtnNodes = append(newMtnNodes, v)
		}

		// see if this node is still in the current node list
		found := false
		for _, n := range currNodes {
			if k == n.NodeName {
				found = true
				break
			}
		}

		// if this item wasn't found, add to the list of removed nodes
		if !found {
			log.Printf("Removing node: %s", k)
			removedNodes = append(removedNodes, v)
		} else {
			// update counts of nodes
			if v.isRiver() {
				numRvrNodes++
			} else if v.isMountain() {
				numMtnNodes++
			} else {
				log.Printf("Error: unknown node class: %s on node: %s", v.Class, v.NodeName)
			}
		}
	}

	// update the nodeCache with the removed nodes
	for _, n := range removedNodes {
		delete(nodeCache, n.NodeName)
	}

	// add the new nodes to console-data
	if len(newNodes) > 0 {
		if ok := ds.dataAddNodes(newNodes); !ok {
			log.Printf("New data send to console-data failed")
			updateSuccess = false
		}
	} else {
		log.Printf("No new nodes to add")
	}

	// remove the nodes from console-data
	if len(removedNodes) > 0 {
		ds.dataRemoveNodes(removedNodes)
	} else {
		log.Printf("No nodes being removed")
	}

	// recalculate the number pods needed and how many assigned to each pod
	// NOTE: do this every time in case something else made changes on the system
	//  like number of console-node replicas deployed
	ns.updateNodeCounts(numMtnNodes, numRvrNodes)

	// make sure the console ssh key has been deployed on all new mountain nodes
	// NOTE: do this last so console-node pods can start to spin up and acquire
	//  nodes while key deployment is happening - may take a while.
	if len(newMtnNodes) > 0 {
		if ok := ensureMountainConsoleKeysDeployed(newMtnNodes); !ok {
			log.Printf("Mountain key deployment failed")
			keySuccess = false
		}
	}

	// return status
	return updateSuccess, keySuccess
}

// Main loop for console-operator stuff
func watchHardware(ds DataService, ns NodeService) {
	// every once in a while send all inventory to update to make sure console-data
	// is actually up to date
	forceUpdateCnt := 0

	// keep track of if the mtn key deployment succeeded
	updateMtnKey := true

	// loop forever looking for updates to the hardware
	for {
		// do a check of the current hardware
		// NOTE: if the service is currently in the process of shutting down
		//  do not perform the hardware update check
		if !inShutdown {
			// do the update
			updateSucess, keySuccess := doHardwareUpdate(ds, ns, forceUpdateCnt == 0, updateMtnKey)

			// set up for next update - normal countdown
			forceUpdateCnt--
			if forceUpdateCnt < 0 {
				// force a complete update every 10 times
				forceUpdateCnt = 10
			}

			// look for failure - override complete update on failure
			if !updateSucess {
				forceUpdateCnt = 0
			}

			// if the mtn key deployment failed, force update next time
			updateMtnKey = !keySuccess
		}

		// There are times we want to wait for a little before starting a new
		// process - ie killproc may get caught trying to kill all instances
		time.Sleep(time.Duration(newHardwareCheckPeriodSec) * time.Second)
	}
}

// Function to read a single env variable into a variable with min/max checks
func readSingleEnvVarInt(envVar string, outVar *int, minVal, maxVal int) {
	// get the env var for maximum number of mountain nodes per pod
	if v := os.Getenv(envVar); v != "" {
		log.Printf("Found %s env var: %s", envVar, v)
		vi, err := strconv.Atoi(v)
		if err != nil {
			log.Printf("Error converting value for %s - expected an integer:%s", envVar, err)
		} else {
			// do some sanity checking
			if vi < minVal {
				log.Printf("Defaulting %s to minimum value:%d", envVar, minVal)
				vi = minVal
			}
			if vi > maxVal {
				log.Printf("Defaulting %s to maximum value:%d", envVar, maxVal)
				vi = maxVal
			}
			*outVar = vi
		}
	}
}

// Main loop for the application
func main() {
	// parse the command line flags to the application
	flag.BoolVar(&debugOnly, "debug", false, "Run in debug only mode, not starting conmand")
	flag.Parse()

	// read the env variables into global vars with min/max sanity checks
	if v := os.Getenv("DEBUG"); v == "TRUE" {
		debugOnly = true
	}
	readSingleEnvVarInt("MAX_MTN_NODES_PER_POD", &maxMtnNodesPerPod, 5, 1500)
	readSingleEnvVarInt("MAX_RVR_NODES_PER_POD", &maxRvrNodesPerPod, 5, 4000)
	readSingleEnvVarInt("HARDWARE_UPDATE_SEC_FREQ", &newHardwareCheckPeriodSec, 10, 14400) // 10 sec -> 4 hrs
	readSingleEnvVarInt("HEARTBEAT_CHECK_SEC_FREQ", &heartbeatCheckPeriodSec, 10, 300)     // 10 sec -> 5 min
	readSingleEnvVarInt("HEARTBEAT_STALE_DURATION_MINUTES", &heartbeatStaleMinutes, 1, 60) // 1 min -> 60 min

	// log the fact if we are in debug mode
	if debugOnly {
		log.Print("Running in DEBUG-ONLY mode.")
	}

	// construct dependency injection
	k8Manager, err := NewK8Manager()
	if err != nil {
		log.Panicf("ERROR: k8Manager failed to initialize")
	}
	slsManager := NewSlsManager()
	nodeManager := NewNodeManager(k8Manager)
	dataManager := NewDataManager(k8Manager, slsManager)
	healthManager := NewHealthManager(dataManager)
	debugManager := NewDebugManager(dataManager, healthManager)

	// Set up the zombie killer
	go watchForZombies()

	// loop over new hardware
	go watchHardware(dataManager, nodeManager)

	// spin a thread to check for stale heartbeat information
	go dataManager.checkHeartbeats()

	// set up a channel to wait for the os to tell us to stop
	// NOTE - must be set up before initializing anything that needs
	//  to be cleaned up.  This will trap any signals and wait to
	//  process them until the channel is read.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)

	setupRoutes(dataManager, healthManager, debugManager)

	// spin the server in a separate thread so main can wait on an os
	// signal to cleanly shut down
	log.Printf("Spinning up http server...")
	httpSrv := http.Server{
		Addr:    httpListen,
		Handler: router,
	}
	go func() {
		// NOTE: do not use log.Fatal as that will immediately exit
		// the program and short-circuit the shutdown logic below
		log.Printf("Info: Server %s\n", httpSrv.ListenAndServe())
	}()
	log.Printf("Info: console-operator API listening on: %v\n", httpListen)

	//////////////////
	// Clean shutdown section
	//////////////////

	// wait here for a signal from the os that we are shutting down
	sig := <-sigs
	inShutdown = true
	log.Printf("Info: Detected signal to close service: %s", sig)

	// stop the server from taking requests
	// NOTE: this waits for active connections to finish
	log.Printf("Info: Server shutting down")
	httpSrv.Shutdown(context.Background())

	log.Printf("Info: Service Exiting.")
}
