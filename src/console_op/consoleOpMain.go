// Copyright 2021 Hewlett Packard Enterprise Development LP

// This file contains the main elements of the application used to
// monitor console applications

package main

import (
	"flag"
	"fmt"
	"log"
	"os/exec"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
)

// global var to help with local running/debugging
var debugOnly bool = false
var command *exec.Cmd = nil

// Function to print information from the k8s cluster
func printK8sInfo() {
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		//panic(err.Error())
		log.Printf("InClusterConfig error: %s", err.Error())
		return
	}

	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		//panic(err.Error())
		log.Printf("NewForConfig error: %s", err.Error())
		return
	}

	// get pods in all the namespaces by omitting namespace
	// Or specify namespace to get pods in particular namespace
	log.Printf("Getting Pods in namespace...")
	pods, err := clientset.CoreV1().Pods("services").List(metav1.ListOptions{})
	if err != nil {
		//panic(err.Error())
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
	log.Printf("Getting cray-conman pod...")
	_, err = clientset.CoreV1().Pods("services").Get("cray-conman", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		fmt.Printf("Pod cray-conman not found in services namespace\n")
	} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
		fmt.Printf("Error getting pod %v\n", statusError.ErrStatus.Message)
	} else if err != nil {
		panic(err.Error())
	} else {
		fmt.Printf("Found cray-conman pod in default namespace\n")
	}

	// get the deployment
	log.Printf("Getting cray-conman deployment")
	dep, err := clientset.AppsV1().Deployments("services").Get("cray-conman", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		fmt.Printf("Pod cray-conman not found in services namespace\n")
	} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
		fmt.Printf("Error getting pod %v\n", statusError.ErrStatus.Message)
	} else if err != nil {
		panic(err.Error())
	} else {
		fmt.Printf("Found cray-conman depolyment in default namespace\n")
		fmt.Printf("  Replicas: %d", dep.Spec.Replicas)
	}

}

// Main loop for the application
func main() {
	// NOTE: this is a work in progress starting to restructure this application
	//  to manage the console state - watching for hardware changes and
	//  updating / restarting the conman process when needed

	// parse the command line flags to the application
	flag.BoolVar(&debugOnly, "debug", false, "Run in debug only mode, not starting conmand")
	flag.Parse()

	// log the fact if we are in debug mode
	if debugOnly {
		log.Print("Running in DEBUG-ONLY mode.")
	}

	// Set up the zombie killer
	go watchForZombies()

	// create a loop to execute the conmand command
	//forceConfigUpdate := true
	for {
		// get the current endpoints from hsm
		currNodes := getCurrentNodes()
		for _, n := range currNodes {
			log.Printf("Node: %s", n.String())
		}

		// get the k8s cluster information (how many workers?, find cray-conman deployment)
		printK8sInfo()

		// modify deployment

		// If this is only debug mode, put a longer wait in here
		if debugOnly {
			// not really running, just give a longer pause before re-running config
			log.Printf("Debugging mode - main loop sleeping")
			time.Sleep(5 * time.Minute)
		}

		// There are times we want to wait for a little before starting a new
		// process - ie killproc may get caught trying to kill all instances
		time.Sleep(10 * time.Second)
	}
}
