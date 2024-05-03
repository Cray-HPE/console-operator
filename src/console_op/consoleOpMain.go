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

func updateCachedNodeData(ds DataService, ns NodeService, updateAll bool) (bool, []nodeConsoleInfo) {
	// return if the console-data update succeeded
	updateSuccessful := true

	// get the current endpoints from hsm
	currNodes := ns.getCurrentNodesFromHSM()
	currNodesMap := make(map[string]nodeConsoleInfo)
	for _, n := range currNodes {
		currNodesMap[n.NodeName] = n
	}

	// Find new nodes that are in the currNodes but not in nodeCache
	var newNodes []nodeConsoleInfo = nil
	for _, n := range currNodes {
		if _, found := nodeCache[n.NodeName]; !found {
			newNodes = append(newNodes, n)
			log.Printf("Found new node: %s", n.String())
		}
	}

	// Find nodes to remove that are in the nodeCache but not in currNodes
	var removedNodes []nodeConsoleInfo = nil
	for _, n := range nodeCache {
		if _, found := currNodesMap[n.NodeName]; !found {
			removedNodes = append(removedNodes, n)
			log.Printf("Removing node: %s", n.String())
		}
	}

	// add the new nodes to console-data
	nodesToUpdate := newNodes
	if updateAll {
		nodesToUpdate = currNodes
		log.Printf("Forcing inventory update of all %d nodes", len(nodesToUpdate))
	}

	if len(nodesToUpdate) > 0 {
		if ok := ds.dataAddNodes(nodesToUpdate); !ok {
			log.Printf("New data send to console-data failed")
			updateSuccessful = false
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

	// If the data updates succeeded we can update the cache
	if updateSuccessful {
		nodeCache = currNodesMap
	}

	// newNodes are returned, not nodesToUpdate because we only want to deploy
	// 		mountain keys for new nodes, not the during the periodic updateAll.
	return updateSuccessful, newNodes
}

// Function to do a hardware update check
func doHardwareUpdate(ds DataService, ns NodeService, updateAll bool, mountainCredsUpdateChannel chan nodeConsoleInfo) bool {
	// record the time of the hardware update attempt
	hardwareUpdateTime = time.Now().Format(time.RFC3339)

	// Update the cache and data in console-data
	updateSuccessful, newNodes := updateCachedNodeData(ds, ns, updateAll)

	// recalculate the number pods needed and how many assigned to each pod
	// NOTE: do this every time in case something else made changes on the system
	//  like number of console-node replicas deployed
	numRvrNodes := 0
	numMtnNodes := 0
	for _, v := range nodeCache {
		// update counts of nodes
		if v.isRiver() {
			numRvrNodes++
		} else if v.isMountain() || v.isParadise() {
			numMtnNodes++
		} else {
			log.Printf("Error: unknown node class: %s on node: %s", v.Class, v.NodeName)
		}
	}
	ns.updateNodeCounts(numMtnNodes, numRvrNodes)

	// Update mountain node keys
	if numMtnNodes > 0 {
		// Generate keys for mountain nodes if needed
		ensureMountainConsoleKeysExist()

		for _, n := range newNodes {
			if n.isMountain() {
				mountainCredsUpdateChannel <- n
			}
		}
	}

	// return status
	return updateSuccessful
}

// Main loop for console-operator stuff
func watchHardware(ds DataService, ns NodeService) {
	// every once in a while send all inventory to update to make sure console-data
	// is actually up to date
	forceUpdateCnt := 0

	// setup routine for pushing mountain keys
	mountainCredsUpdateChannel := make(chan nodeConsoleInfo, 100)
	go doMountainCredsUpdates(mountainCredsUpdateChannel)

	// loop forever looking for updates to the hardware
	for {
		// do a check of the current hardware
		// NOTE: if the service is currently in the process of shutting down
		//  do not perform the hardware update check
		if !inShutdown {
			// do the update
			updateSuccessful := doHardwareUpdate(ds, ns, forceUpdateCnt == 0, mountainCredsUpdateChannel)

			// set up for next update - normal countdown
			forceUpdateCnt--
			if forceUpdateCnt < 0 {
				// force a complete update every 10 times
				forceUpdateCnt = 10
			}

			// look for failure - override complete update on failure
			if !updateSuccessful {
				forceUpdateCnt = 0
			}
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
