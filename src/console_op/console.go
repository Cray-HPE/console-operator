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
	"fmt"
	"io"
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

// InputStreamer - handle the input stream from the websocket
type InputStreamer struct {
	conn   *websocket.Conn
	reader io.Reader
}

func (l *InputStreamer) Read(p []byte) (n int, err error) {
	if l.reader == nil {
	}
	n, err = l.reader.Read(p)
	log.Printf(" Reading from : %d, %b", n, p)
	return n, err
}

// NewInputStreamer - make a new input streamer based on this websocket
func NewInputStreamer(conn *websocket.Conn) InputStreamer {
	log.Print("WEBSOCKET:: Making InputStreamer")
	var l InputStreamer
	l.conn = conn
	readType, connReader, err := l.conn.NextReader()
	log.Printf("WEBSOCKET:: Connection Reader Type: %d", readType)
	if err != nil {
		log.Printf("WEBSOCKET:: Error getting next reader: %v", err)
	}
	l.reader = connReader
	return l
}

// OutputStreamer - handle the output stream to the websocket
type OutputStreamer struct {
	conn *websocket.Conn
}

func (l *OutputStreamer) String() string {
	// we only care about streaming to the websocket connection - stub this out
	return ""
}

func (l *OutputStreamer) Write(p []byte) (n int, err error) {
	l.conn.WriteMessage(websocket.TextMessage, p)
	return len(p), nil
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
	cmd := []string{"conman", "-j", xname}
	//cmd := []string{"ls", "-la", "/var/log/conman"}

	// Execute the command in the pod
	log.Printf("WEBSOCKET:: creating request")
	req := cs.k8s.getClientSet().CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace("services").
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Command:   cmd,
			Container: "cray-console-node",
			Stdin:     true,
			Stdout:    true,
			Stderr:    false,
			TTY:       true,
		}, scheme.ParameterCodec)

	log.Printf("WEBSOCKET:: creating executor")
	config, err := rest.InClusterConfig()
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		log.Printf("failed to create executor: %v", err)
	}

	// define the I/O buffers
	//var stdin bytes.Buffer

	//log.Printf("WEBSOCKET:: Starting input handler")
	//go func() {
	//	defer func() {
	//		log.Printf("WEBSOCKET:: USER->NODE exiting input handler thread")
	//	}()

	//	// append input lines to the file
	//	log.Printf("WEBSOCKET:: USER->NODE Starting read loop")
	//	for {
	//		//get the next input line
	//		_, message, err := conn.ReadMessage()
	//		if err != nil {
	//			log.Printf("  WEBSOCKET:: USER->NODE Error reading input message %v", err)
	//			break
	//		}

	//		// append to the file
	//		log.Printf("  WEBSOCKET:: USER->NODE Received input line: %s", message)
	//		n, err := stdin.Write(message)
	//		if err != nil {
	//			log.Printf("  WEBSOCKET:: USER->NODE error writing to pod stream: %v", err)
	//		} else {
	//			log.Printf("  WEBSOCKET:: USER->NODE wrote %d bytes to pod stream.", n)
	//		}
	//	}
	//}()

	o := &OutputStreamer{}
	o.conn = conn

	l := NewInputStreamer(conn)
	//readType, connReader, err := conn.NextReader()
	//log.Printf("WEBSOCKET:: Connection Reader Type: %d", readType)
	//if err != nil {
	//	log.Printf("WEBSOCKET:: Error getting next reader: %v", err)
	//	conn.Close()
	//	return
	//}

	log.Printf("WEBSOCKET:: starting command stream")
	err = executor.Stream(remotecommand.StreamOptions{
		Stdin:  &l,
		Stdout: o,
		Stderr: nil,
		Tty:    true,
		//Tty:    true,
	})
	if err != nil {
		log.Printf("WEBSOCKET:: failed to execute command in pod: %v", err)
		//cancel()
		return
	}

	log.Printf("WEBSOCKET:: Shutting down")

	// shut down the context, then close the connection
	//cancel()
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

	// find which container is monitoring this node
	podName, err := cs.dataService.getNodePodForXname(xname)

	// upgrade https to secure websocket connection
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Error upgrading:", err)
		return
	}
	defer func() {
		log.Printf("WEBSOCKET:: Doing deferred close")
	}()

	// Build the command to be executed in the pod
	//cmd := []string{"conman", "-j", xname}
	cmd := []string{"ls", "-la", "/var/log/conman/*x3*"}

	// Execute the command in the pod
	log.Printf("WEBSOCKET:: creating request")
	req := cs.k8s.getClientSet().CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace("services").
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Command:   cmd,
			Container: "cray-console-node",
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	log.Printf("WEBSOCKET:: creating executor")
	config, err := rest.InClusterConfig()
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		log.Printf("failed to create executor: %v", err)
	}

	o := &OutputStreamer{}
	o.conn = conn
	e := &OutputStreamer{}
	e.conn = conn

	log.Printf("WEBSOCKET:: starting command stream")
	//ctx, cancel := context.WithCancel(context.Background())
	err = executor.Stream(remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: o,
		Stderr: e,
		Tty:    false,
	})
	if err != nil {
		log.Printf("WEBSOCKET:: failed to execute command in pod: %v", err)
		//cancel()
		return
	}

	log.Printf("WEBSOCKET:: completed command stream")

	// 200 ok
	SendResponseJSON(w, http.StatusOK, nil)
}
