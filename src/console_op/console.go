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
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	v1 "k8s.io/api/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
)

// DebugLog - set up debug logging if the env variable is set
var DebugLog bool = os.Getenv("DebugLog") == "TRUE"

func writeDebugLog(format string, v ...any) {
	if DebugLog {
		log.Printf(format, v...)
	}
}

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

// used for upgrading the http connection to a websocket
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

////////////////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////////////////
// IOStreamer
////////////////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////////////////

// IOStreamer - handle the input/output streams from the websocket
type IOStreamer struct {
	// NOTE: for the time being the removal of input from the output steam doesn't work,
	//  but I am going to leave it in place if we get back to trying to fix it
	conn         *websocket.Conn
	mu           sync.Mutex
	inputStrings [][]byte
}

func (l *IOStreamer) addInputString(msg []byte) {
	l.inputStrings = append(l.inputStrings, msg)
}

func (l *IOStreamer) removeInputStrings(msg []byte) []byte {
	// find all input strings in msg and remove them
	retVal := bytes.Clone(msg)
	for i, inStr := range l.inputStrings {
		writeDebugLog("WEBSOCKET::RemoveInputStrings: retVal:%s inStr:%s", string(retVal), string(inStr))
		beforeStr, afterStr, found := bytes.Cut(retVal, inStr)
		if found {
			writeDebugLog("  Found input string")
			// remove the element from the input strings
			l.inputStrings = append(l.inputStrings[:i], l.inputStrings[i+1:]...)

			// concatenate the before and after bits
			if len(beforeStr) > 0 && len(afterStr) > 0 {
				retVal = append(beforeStr, afterStr...)
			} else if len(beforeStr) > 0 {
				retVal = bytes.Clone(beforeStr)
			} else if len(afterStr) > 0 {
				retVal = bytes.Clone(afterStr)
			} else {
				// nothing left
				writeDebugLog("  All input removed")
				return nil
			}
			writeDebugLog("  New retVal:%s", string(retVal))
		} else {
			writeDebugLog("  No input found")
		}
	}

	return retVal
}

func (l *IOStreamer) Read(p []byte) (n int, err error) {
	// NOTE: the intent is to remove the input stream contents from the
	//  output stream so they don't get printed twice. It isn't quite working
	//  yet, but I am leaving it in place for now.
	//l.mu.Lock()
	//defer l.mu.Unlock()

	// Read the next message from the websocket connection
	msgType, msgArr, err := l.conn.ReadMessage()
	writeDebugLog("WEBSOCKET::Read Read: type:%d, len:%d, bytes:%s", msgType, len(msgArr), msgArr[:n])

	// keep this message to remove from write stream to avoid double writing
	//l.addInputString(msgArr)

	// The newline gets stripped off by the websocket - add it back
	// NOTE - without this the command will not be executed on the remote terminal
	enhanced := append(msgArr, "\n"...)
	copy(p, enhanced)
	return len(enhanced), err
}

func (l *IOStreamer) String() string {
	// we only care about streaming to the websocket connection - stub this out
	return ""
}

func (l *IOStreamer) writeMessage(msg []byte) error {
	// if there is nothing to write, don't try to write
	if len(msg) == 0 {
		log.Print("WEBSOCKET::Writing::Message - Empty String")
		return nil
	}
	writeDebugLog("WEBSOCKET::Writing::Message: %s", string(msg))
	err := l.conn.WriteMessage(websocket.TextMessage, msg)
	if err != nil {
		log.Printf("WEBSOCKET::Writing::ERROR: %v", err)
	}
	return err
}

func (l *IOStreamer) Write(p []byte) (n int, err error) {
	// NOTE: the intent is to remove the input stream contents from the
	//  output stream so they don't get printed twice. It isn't quite working
	//  yet, but I am leaving it in place for now.
	//l.mu.Lock()
	//defer l.mu.Unlock()

	// remove any input commands so they don't print twice in the output
	inStr := bytes.Clone(p)
	//inStr = l.removeInputStrings(inStr)

	// Process the remaining strings
	n = len(inStr)
	err = l.writeMessage(inStr)

	return n, err
}

// Finds and returns the node where the given pod is running within the k8s cluster.
func (cs ConsoleManager) doInteractConsole(w http.ResponseWriter, r *http.Request) {
	writeDebugLog("WEBSOCKET:: Interact Console")

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
		writeDebugLog("WEBSOCKET:: Doing deferred close")
	}()

	// find which container is monitoring this node
	podName, err := cs.dataService.getNodePodForXname(xname)

	// Build the command to be executed in the pod
	cmd := []string{"conman", "-j", xname}

	// Execute the command in the pod
	writeDebugLog("WEBSOCKET:: creating request")
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

	writeDebugLog("WEBSOCKET:: creating executor")
	config, err := rest.InClusterConfig()
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		log.Printf("failed to create executor: %v", err)
	}

	// object to connect websocket streams to executor io
	webIO := &IOStreamer{conn: conn}

	writeDebugLog("WEBSOCKET:: starting command stream")
	err = executor.Stream(remotecommand.StreamOptions{
		Stdin:  webIO,
		Stdout: webIO,
		Stderr: nil,
		Tty:    true,
	})
	if err != nil {
		log.Printf("WEBSOCKET:: failed to execute command in pod: %v", err)
		return
	}

	// close the connection
	writeDebugLog("WEBSOCKET:: Shutting down connection")
	conn.Close()

	writeDebugLog("WEBSOCKET:: Exiting websocket")
}

// Finds and returns the node where the given pod is running within the k8s cluster.
func (cs ConsoleManager) doFollowConsole(w http.ResponseWriter, r *http.Request) {
	// This is accessed with a connection that can be upgraded to a websocket.
	writeDebugLog("WEBSOCKET:: Follow Console")

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
		writeDebugLog("WEBSOCKET:: Doing deferred close")
	}()

	// find which container is monitoring this node
	podName, err := cs.dataService.getNodePodForXname(xname)

	// start building the command options
	cmd := []string{"tail"}

	// Find if this is following or just dumping the log
	if r.Header.Get("X-DUMP-ONLY") != "True" {
		cmd = append(cmd, "-F")
	}

	// Find if there is a number of lines to display
	numLines := r.Header.Get("X-TAIL")
	if numLines != "" {
		cmd = append(cmd, "-n", numLines)
	}

	// add the filename to the command
	filename := fmt.Sprintf("/var/log/conman/console.%s", xname)
	cmd = append(cmd, filename)

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

	writeDebugLog("WEBSOCKET:: creating executor")
	config, err := rest.InClusterConfig()
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		log.Printf("failed to create executor: %v", err)
	}

	// object to connect websocket streams to executor io
	webIO := &IOStreamer{conn: conn}

	writeDebugLog("WEBSOCKET:: starting command stream")
	err = executor.Stream(remotecommand.StreamOptions{
		Stdin:  webIO,
		Stdout: webIO,
		Stderr: nil,
		Tty:    true,
	})
	if err != nil {
		log.Printf("WEBSOCKET:: failed to execute command in pod: %v", err)
		return
	}

	// close the connection
	writeDebugLog("WEBSOCKET:: Shutting down connection")
	conn.Close()

	writeDebugLog("WEBSOCKET:: Exiting websocket")
}
