//
//  MIT License
//
//  (C) Copyright 2025 Hewlett Packard Enterprise Development LP
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

// Interactions with the consoles

package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"

	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	v1 "k8s.io/api/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
)

// ConsoleService interface for interacting with the consoles themselves
type ConsoleService interface {
	doFollowConsole(w http.ResponseWriter, r *http.Request)
	doInteractConsole(w http.ResponseWriter, r *http.Request)
}

// ConsoleManager implements a ConsoleService
type ConsoleManager struct {
	k8s         K8Service
	dataService DataService
}

// NewConsoleManager factory function to create a new ConsoleService
func NewConsoleManager(k8s K8Service, ds DataService) ConsoleService {
	return &ConsoleManager{k8s: k8s, dataService: ds}
}

var clients = make(map[*websocket.Conn]bool) // Connected clients
var broadcast = make(chan []byte)            // Broadcast channel
var mutex = &sync.Mutex{}                    // Protect clients map

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Finds and returns the node where the given pod is running within the k8s cluster.
func (cs ConsoleManager) doInteractConsole(w http.ResponseWriter, r *http.Request) {
	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// `/console-operator/interact/{nodeXname}` - pull out the node being interacted with
	xname := chi.URLParam(r, "nodeXname")
	if xname == "" {
		log.Printf("There was an error reading the node xname from the request %s", r.URL.Path)
		var body = BaseResponse{
			Msg: fmt.Sprintf("There was an error reading the node xname from the request %s", r.URL.Path),
		}
		SendResponseJSON(w, http.StatusBadRequest, body)
		return
	}

	// upgrade https to secure websocket connection
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Error upgrading:", err)
		return
	}
	defer func() {
		log.Printf("WEBSOCKET:: Doing deferred close")
	}()

	// find which container is monitoring this node
	podName, err := cs.dataService.getNodePodForXname(xname)

	// Build the command to be executed in the pod
	cmd := []string{"sh"}

	// Execute the command in the pod
	log.Printf("WEBSOCKET:: creating request")
	req := cs.k8s.getClientSet().CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace("services").
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Command: cmd,
			Stdin:   true,
			Stdout:  true,
			Stderr:  true,
			TTY:     true,
		}, scheme.ParameterCodec)

	log.Printf("WEBSOCKET:: creating executor")
	config, err := rest.InClusterConfig()
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		log.Printf("failed to create executor: %v", err)
	}

	log.Printf("WEBSOCKET:: starting streamWithContext")
	var stdin, stdout bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  &stdin,
		Stdout: &stdout,
		Tty:    true,
	})
	if err != nil {
		log.Printf("failed to execute command in pod: %v", err)
		cancel()
		return
	}

	// take any output from the file and output to the websocket
	log.Printf("WEBSOCKET:: Starting piping output")
	go func() {
		// Log when this thread goes away
		defer func() {
			log.Printf("WEBSOCKET:: exiting tailing thread")
		}()

		log.Printf("WEBSOCKET:: Starting output loop")
		for {
			// pull in the next line of input from the user
			line, err := stdout.ReadString('\n')
			if err != nil {
				log.Println("Error Reading stdout message")
				break
			}
			log.Printf("  WEBSOCKET:: Read line: %s", line)
			if line != "" {
				outMsg := []byte(fmt.Sprintf("%s: %s", xname, line))
				if err := conn.WriteMessage(websocket.TextMessage, outMsg); err != nil {
					log.Println("Error writing message to websocket:", err)
					break
				}
			}
		}
	}()

	log.Printf("WEBSOCKET:: Starting input handler")
	// append input lines to the file
	log.Printf("WEBSOCKET:: Starting read loop")
	for {
		//get the next input line
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("Error reading message:", err)
			break
		}

		// append to the file
		log.Printf("  WEBSOCKET:: Received input line: %s", message)
		stdin.Write(message)
	}
	log.Printf("WEBSOCKET:: Shutting down")

	// shut down the context, then close the connection
	cancel()
	conn.Close()

	log.Printf("WEBSOCKET:: Exiting websocket")
}

// Finds and returns the node where the given pod is running within the k8s cluster.
func (cs ConsoleManager) doFollowConsole(w http.ResponseWriter, r *http.Request) {
	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// `/console-operator/follow/{nodeXname}`
	xname := chi.URLParam(r, "nodeXname")
	if xname == "" {
		log.Printf("There was an error reading the node xname from the request %s", r.URL.Path)
		var body = BaseResponse{
			Msg: fmt.Sprintf("There was an error reading the node xname from the request %s", r.URL.Path),
		}
		SendResponseJSON(w, http.StatusBadRequest, body)
		return
	}

	// 200 ok
	SendResponseJSON(w, http.StatusOK, nil)
}
