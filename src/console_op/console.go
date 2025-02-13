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

// IOStreamer - handle the input/output streams from the websocket
type IOStreamer struct {
	// NOTE: for the time being the removal of input from the output steam doesn't work,
	//  but I am going to leave it in place if we get back to trying to fix it
	conn          *websocket.Conn
	writeFragment []byte
	mu            sync.Mutex
	inputStrings  [][]byte
}

func (l *IOStreamer) addInputString(msg []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inputStrings = append(l.inputStrings, msg)
}

func (l *IOStreamer) removeInputStrings(msg []byte) []byte {
	l.mu.Lock()
	defer l.mu.Unlock()

	// find all input strings in msg and remove them
	retVal := bytes.Clone(msg)
	for i, inStr := range l.inputStrings {
		beforeStr, afterStr, found := bytes.Cut(retVal, inStr)
		if found {
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
				return nil
			}
		}
	}

	return retVal
}

func (l *IOStreamer) Read(p []byte) (n int, err error) {
	readType, connReader, err := l.conn.NextReader()
	writeDebugLog("WEBSOCKET:: Connection Reader Type: %d", readType)
	if err != nil {
		log.Printf("WEBSOCKET:: Error getting next reader: %v", err)
		return 0, err
	}
	n, err = connReader.Read(p)
	//l.addInputString(p[:n])
	return n, err
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
	err := l.conn.WriteMessage(websocket.TextMessage, msg)
	if err != nil {
		log.Printf("WEBSOCKET::Writing::ERROR: %v", err)
	}
	return err
}

func (l *IOStreamer) Write(p []byte) (n int, err error) {
	// Write writes len(p) bytes from p to the underlying data stream.
	// It returns the number of bytes written from p (0 <= n <= len(p))
	// and any error encountered that caused the write to stop early.
	// Write must return a non-nil error if it returns n < len(p).
	// Write must not modify the slice data, even temporarily.

	n = len(p)

	// break into separate sections based on CR/LF pairs and separate CR
	// if the ending portion is not and EOL, save for the next read

	// tack on the last remnant if this is one
	inStr := bytes.Clone(p)
	if l.writeFragment != nil {
		writeDebugLog("WEBSOCKET::Writing: appending fragment %s", string(l.writeFragment))
		inStr = append(l.writeFragment, inStr...)
		l.writeFragment = nil
	}

	// remove any input commands so they don't print twice in the output
	inStr = l.removeInputStrings(inStr)

	// Process the remaining strings
	for len(inStr) > 0 {
		// split at the first CR/LF found
		before, after, found := bytes.Cut(inStr, []byte{13, 10})
		writeDebugLog("WEBSOCKET::Writing::Cut before:%s, after:%s, found:%t", string(before), string(after), found)
		inStr = after
		if len(before) == 0 {
			// lopping off CRLF at the beginning of the string
			continue
		}

		// Handle outputting the content before the CRLF
		if !found {
			writeDebugLog("WEBSOCKET::Writing: CRLF not found")
			// end of input string - set up for next time
			if bytes.ContainsRune(before, '#') || bytes.ContainsRune(before, ':') {
				// probably a command prompt - write it
				err = l.writeMessage(before)
				if err != nil {
					break
				}
			} else {
				// prepend to the next write call so it gets written to a EOL
				writeDebugLog("WEBSOCKET::Writing: preserving write fragment")
				l.writeFragment = bytes.Clone(before)
			}
		} else {
			// process the before string fragment as a separate line - may have independent LF
			// NOTE - this only handles one LF in the fragment - may need to beef this up?
			writeDebugLog("WEBSOCKET::Writing: processing input")
			beforeLF, afterLF, _ := bytes.Cut(before, []byte{10})
			writeDebugLog("WEBSOCKET::Writing: writing beforeLF %s", string(beforeLF))
			err = l.writeMessage(beforeLF)
			if err != nil {
				break
			}
			if len(afterLF) > 0 {
				writeDebugLog("WEBSOCKET::Writing: writing afterLF %s", string(afterLF))
				err = l.writeMessage(afterLF)
				if err != nil {
					break
				}
			}
		}
	}

	writeDebugLog("WEBSOCKET::Writing: exiting %d, %v", n, err)

	return n, err
}

// Finds and returns the node where the given pod is running within the k8s cluster.
func (cs ConsoleManager) doInteractConsole(w http.ResponseWriter, r *http.Request) {
	// This is accessed with a connection that can be upgraded to a websocket. It was tested
	// using 'websocat' as 'curl' wasn't sufficient:
	// websocat -H "Authorization: Bearer ${TOKEN}" --request-uri https://api_gw_service.local wss://api_gw_service.local/apis/console-operator/console-operator/interact/xname

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
	// This is accessed with a connection that can be upgraded to a websocket. It was tested
	// using 'websocat' as 'curl' wasn't sufficient:
	// websocat -H "Authorization: Bearer ${TOKEN}" --request-uri https://api_gw_service.local wss://api_gw_service.local/apis/console-operator/console-operator/interact/xname

	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// NOTE - leaving commented out until craycli is working and need
	//  to figure out how to transfer options in websocket call
	// read the request data - must be in json content
	//reqBody, err := io.ReadAll(r.Body)
	//defer r.Body.Close()
	//if err != nil {
	//	log.Printf("There was an error reading the request body: S%s\n", err)
	//	var body = BaseResponse{
	//		Msg: fmt.Sprintf("There was an error reading the request body: S%s", err),
	//	}
	//	SendResponseJSON(w, http.StatusBadRequest, body)
	//	return
	//}
	//contentType := r.Header.Get("Content-type")
	//log.Printf("Content-Type: %s\n", contentType)
	//if contentType != "application/json" {
	//	var body = BaseResponse{
	//		Msg: fmt.Sprintf("Expecting Content-Type: application/json"),
	//	}
	//	SendResponseJSON(w, http.StatusBadRequest, body)
	//	return
	//}
	//writeDebugLog("request data: %s\n", string(reqBody))

	//var inData GetNodeData
	//err = json.Unmarshal(reqBody, &inData)
	//if err != nil {
	//	log.Printf("There was an error while decoding the json data: %s\n", err)
	//	var body = BaseResponse{
	//		Msg: fmt.Sprintf("There was an error while decoding the json data: %s", err),
	//	}
	//	SendResponseJSON(w, http.StatusBadRequest, body)
	//	return
	//}

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

	// TODO - once craycli is passing options:
	//  - Follow or dump/exit
	//  - Number of lines in -n option

	// Build the command to be executed in the pod
	filename := fmt.Sprintf("/var/log/conman/console.%s", xname)
	cmd := []string{"tail", "-F", "-n 20", filename}

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
