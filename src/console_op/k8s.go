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

// This file contains the interactions with k8s

package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
)

// File to hold target number of node information - it will reside on
// a shared file system so console-node pods can read what is set here
const targetNodeFile string = "/var/log/console/TargetNodes.txt"

type K8Service interface {
	printK8sInfo()
	getReplicaCount() (replicaCnt int, err error)
	updateReplicaCount(newReplicaCnt int)
	updateNodesPerPod(newNumMtn, newNumRvr int)
	getPodLocationAlias(podID string) (loc string, err error)
}

// Implements K8Service
type K8Manager struct {
	config    *rest.Config
	clientset *kubernetes.Clientset
}

func NewK8Manager() (*K8Manager, error) {
	// creates the in-cluster config
	var err error
	var config *rest.Config = nil
	var clientset *kubernetes.Clientset = nil
	config, err = rest.InClusterConfig()
	if err != nil {
		log.Printf("InClusterConfig error: %s", err.Error())
		return nil, err
	}
	// creates the clientset
	clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		log.Printf("NewForConfig error: %s", err.Error())
		return nil, err
	}

	return &K8Manager{config: config, clientset: clientset}, nil
}

// Function to print information from the k8s cluster
func (k8s K8Manager) printK8sInfo() {
	// NOTE: not needed for production, but nice debug code to keep around

	// make sure k8s is initialized
	if k8s.clientset == nil || k8s.config == nil {
		log.Printf("ERROR: k8s not initialized correctly")
		return
	}

	// Or specify namespace to get pods in particular namespace
	log.Printf("Getting Pods in namespace...")
	pods, err := k8s.clientset.CoreV1().Pods("services").List(metav1.ListOptions{})
	if err != nil {
		log.Printf("PodsList error: %s", err.Error())
	}
	log.Printf("There are %d pods in the services namespace in the cluster\n", len(pods.Items))

	// print details on each pod found
	for _, pod := range pods.Items {
		log.Printf("Pod: %s", pod.GetName())
	}

	// Examples for error handling:
	// - Use helper functions e.g. errors.IsNotFound()
	// - And/or cast to StatusError and use its properties like e.g. ErrStatus.Message
	log.Printf("Getting cray-console-node pods...")
	_, err = k8s.clientset.CoreV1().Pods("services").Get("cray-console-node", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		log.Printf("Pod cray-console-node not found in services namespace\n")
	} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
		log.Printf("Error getting pod %v\n", statusError.ErrStatus.Message)
	} else if err != nil {
		log.Printf("Error getting pod: %s", err.Error())
	} else {
		fmt.Printf("Found cray-conman pod in default namespace\n")
	}

}

// Grab the current number of console-node replicas from k8s
func (k8s K8Manager) getReplicaCount() (replicaCnt int, err error) {
	// get the stateful set
	consoleNodeRepCount := -1
	dep, err := k8s.clientset.AppsV1().StatefulSets("services").Get("cray-console-node", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		log.Printf("StatefulSet cray-console-node not found in services namespace\n")
		return consoleNodeRepCount, err
	} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
		log.Printf("Error getting statefulSet cray-console-node in services namespace: %v\n", statusError.ErrStatus.Message)
		return consoleNodeRepCount, err
	} else if err != nil {
		log.Printf("Unknown error getting statefulSet cray-console-node in services namespace: %s", err.Error())
		return consoleNodeRepCount, err
	}

	consoleNodeRepCount = int(*dep.Spec.Replicas)
	return consoleNodeRepCount, nil
}

// Function to update the number of console-node replicas
func (k8s K8Manager) updateReplicaCount(newReplicaCnt int) {
	// This function interacts with k8s to check the current number of replicas
	// in the console-node statefulset.  It will change the replica count to
	// match what it should be creating new pods or destroying current ones.

	// ensure that k8s was initialized correctly
	if k8s.clientset == nil || k8s.config == nil {
		log.Printf("ERROR: k8s not initialized correctly")
		return
	}

	// get the stateful set
	dep, err := k8s.clientset.AppsV1().StatefulSets("services").Get("cray-console-node", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		log.Printf("StatefulSet cray-console-node not found in services namespace\n")
		return
	} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
		log.Printf("Error getting statefulSet %v\n", statusError.ErrStatus.Message)
		return
	} else if err != nil {
		log.Printf("Unknown error getting statefulSet: %s", err.Error())
		return
	}

	// Find the current number of replicas in the deployment
	currReplicas := *dep.Spec.Replicas
	log.Printf("Current console-node replicas: %d, Requested replicas: %d", currReplicas, newReplicaCnt)

	// if the numbers don't match, update the replica count
	if int32(newReplicaCnt) != currReplicas {
		// update deployment to the desired number
		*dep.Spec.Replicas = int32(newReplicaCnt)
		newDep, err := k8s.clientset.AppsV1().StatefulSets("services").Update(dep)
		if err != nil {
			// NOTE - do not reset numNodePods if this failed, that should trigger
			//  a retry the next time it checks
			log.Printf("Error updating deployment: %s", err.Error())
			return
		}
		log.Printf("  Updated stateful set to %d replicas", *newDep.Spec.Replicas)
	} else {
		log.Printf("  Already correct number of replicas in deployment")
	}

	// only set the global number when successful
	numNodePods = newReplicaCnt
}

// keep track of the number of file access errors
var numFileErrors = 0

// Update the number of consoles per node pod
func (K8Manager) updateNodesPerPod(newNumMtn, newNumRvr int) {
	// NOTE: for the time being we will just put this information
	//  into a simple text file on a pvc shared with console-operator
	//  and console-node pods.  The console-operator will write changes
	//  and the console-node pods will read periodically for changes.
	//  This mechanism can be made more elegant later if needed but it
	//  needs to be something that can be picked up by all console-node
	//  pods without restarting them.  It is complicated to update all
	//  running pods through a direct rest interface...

	// make sure the directory exists to put the file in place
	pos := strings.LastIndex(targetNodeFile, "/")
	if pos < 0 {
		log.Printf("Error: incorrect target node file name: %s", targetNodeFile)
		return
	}
	targetNodeDir := targetNodeFile[:pos]
	if _, err := os.Stat(targetNodeDir); os.IsNotExist(err) {
		log.Printf("Target node directory does not exist, creating: %s", targetNodeDir)
		err = os.MkdirAll(targetNodeDir, 0766)
		if err != nil {
			// If we have too many attempts fail, complain loudly
			if numFileErrors > 3 {
				log.Panicf("Multiple file access errors, unable to create dir: %s", err)
			}
			log.Printf("Unable to create dir: %s", err)
			numFileErrors += 1
			return
		}
	}

	// open the file for writing
	log.Printf("Opening target node file for output: %s", targetNodeFile)
	cf, err := os.OpenFile(targetNodeFile, os.O_TRUNC|os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		// If we have too many attempts fail, complain loudly
		if numFileErrors > 3 {
			log.Panicf("Multiple file access errors, unable to open config file to write: %s", err)
		}
		log.Printf("Error: Unable to open config file to write: %s", err)
		numFileErrors += 1
		return
	}

	// reset the file error count and make sure file gets closed
	numFileErrors = 0
	defer cf.Close()

	// The file only consists of two lines, write them
	cf.WriteString(fmt.Sprintf("River:%d\n", newNumRvr))
	cf.WriteString(fmt.Sprintf("Mountain:%d\n", newNumMtn))

	// only update the stored values after correctly set in file - this should
	// trigger a retry if something goes wrong
	numMtnNodesPerPod = newNumMtn
	numRvrNodesPerPod = newNumRvr
}

// Find and return where the current pod is running in k8s
func (k8s K8Manager) getPodLocationAlias(podID string) (loc string, err error) {
	pod, err := k8s.clientset.CoreV1().Pods("services").Get(podID, metav1.GetOptions{})
	if err != nil {
		log.Printf("Error: Unable to find the node for pod %s, %s", podID, err)
		return "", err
	}

	loc = pod.Spec.NodeName
	return loc, err
}
