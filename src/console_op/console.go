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
	"log"
	"net/http"
	"os"

	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/hpcloud/tail"
)

// ConsoleService interface for interacting with the consoles themselves
type ConsoleService interface {
	doFollowConsole(w http.ResponseWriter, r *http.Request)
	doInteractConsole(w http.ResponseWriter, r *http.Request)
}

// ConsoleManager implements a ConsoleService
type ConsoleManager struct {
	k8Service K8Service
}

// NewConsoleManager factory function to create a new ConsoleService
func NewConsoleManager(k8s K8Service) ConsoleService {
	return &ConsoleManager{k8Service: k8s}
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
		conn.Close()
	}()

	// create the input file and push something out there.
	inputFile, err := os.Create("/tmp/test.txt")
	inputFile.WriteString("Starting")
	inputFile.Sync()

	log.Printf("WEBSOCKET:: Starting input handler")
	go func() {
		// close the file when done
		defer func() {
			inputFile.Close()
		}()

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
			outMsg := fmt.Sprintf("%s: %s", xname, message)
			log.Printf("  WEBSOCKET:: Received input line: %s", outMsg)
			inputFile.WriteString(outMsg)
			inputFile.Sync()
		}
	}()

	// take any output from the file and output to the websocket
	log.Printf("WEBSOCKET:: Starting file tailing")
	t, err := tail.TailFile(
		"/tmp/test.txt",
		tail.Config{Follow: true, Location: &tail.SeekInfo{Offset: 0, Whence: 2}},
	)
	if err != nil {
		log.Fatalf("tail file err: %v", err)
	}

	log.Printf("WEBSOCKET:: Starting tail loop")
	for line := range t.Lines {
		log.Printf("  WEBSOCKET:: Read line: %s", line.Text)
		if line.Text != "" {
			outMsg := []byte(fmt.Sprintf("%s: %s", xname, line.Text))
			if err := conn.WriteMessage(websocket.TextMessage, outMsg); err != nil {
				log.Println("Error writing message:", err)
				break
			}
		}
	}

	log.Printf("WEBSOCKET:: Exiting websocket")
	// Forward input to the console
	//for {
	// Read message from the client
	//	_, message, err := conn.ReadMessage()
	//	if err != nil {
	//		log.Println("Error reading message:", err)
	//		break
	//	}
	//	fmt.Printf("Received: %s\\n", message)
	//	// Echo the message back to the client
	//	outMsg := []byte(fmt.Sprintf("%s: %s", xname, message))
	//	if err := conn.WriteMessage(websocket.TextMessage, outMsg); err != nil {
	//		log.Println("Error writing message:", err)
	//		break
	//	}
	//}
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
