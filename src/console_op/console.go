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

// used for upgrading the http connection to a websocket
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// IOStreamer - handle the input/output streams from the websocket
type IOStreamer struct {
	conn          *websocket.Conn
	writeFragment []byte
}

func (l *IOStreamer) Read(p []byte) (n int, err error) {
	_, connReader, err := l.conn.NextReader()
	//log.Printf("WEBSOCKET:: Connection Reader Type: %d", readType)
	if err != nil {
		log.Printf("WEBSOCKET:: Error getting next reader: %v", err)
		return 0, err
	}
	n, err = connReader.Read(p)
	log.Printf(" WEBSOCKET::Reading: %d, ::%s::", n, p[:n])
	log.Printf(" WEBSOCKET::Reading: %d, ::%d::", n, p[:n])
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

	//msg := string(p) + "\n"
	//l.conn.WriteMessage(websocket.TextMessage, []byte(msg))
	//return len(msg), nil
	n = len(p)

	log.Printf("WEBSOCKET::Writing: input len %d", n)

	// break into separate sections based on CR/LF pairs and separate CR
	// if the ending portion is not and EOL, save for the next read

	// tack on the last remnant if this is one
	inStr := p
	if l.writeFragment != nil {
		log.Printf("WEBSOCKET::Writing: appending fragment %s", string(l.writeFragment))
		inStr = append(l.writeFragment, inStr...)
		l.writeFragment = nil
	}

	for len(inStr) > 0 {
		// split at the first CR/LF found
		before, after, found := bytes.Cut(inStr, []byte{13, 10})
		log.Printf("WEBSOCKET::Writing::Cut before:%s, after:%s, found:%t", string(before), string(after), found)
		inStr = after
		if len(before) == 0 {
			// lopping off CRLF at the beginning of the string
			continue
		}

		// Handle outputting the content before the CRLF
		if !found {
			log.Print("WEBSOCKET::Writing: CRLF not found")
			// end of input string - set up for next time
			if bytes.ContainsRune(before, '#') {
				// probably a command prompt - write it
				err = l.writeMessage(before)
				if err != nil {
					break
				}
			} else {
				// prepend to the next write call so it gets written to a EOL
				log.Print("WEBSOCKET::Writing: preserving write fragment")
				l.writeFragment = bytes.Clone(before)
			}
		} else {
			// process the before string fragment as a separate line - may have independent LF
			// NOTE - this only handles one LF in the fragment - may need to beef this up?
			log.Print("WEBSOCKET::Writing: processing input")
			beforeLF, afterLF, _ := bytes.Cut(before, []byte{10})
			log.Printf("WEBSOCKET::Writing: writing beforeLF %s", string(beforeLF))
			err = l.writeMessage(beforeLF)
			if err != nil {
				break
			}
			if len(afterLF) > 0 {
				log.Printf("WEBSOCKET::Writing: writing afterLF %s", string(afterLF))
				err = l.writeMessage(afterLF)
				if err != nil {
					break
				}
			}
		}
	}

	log.Printf("WEBSOCKET::Writing: exiting %d, %v", n, err)

	return n, err
}

// Finds and returns the node where the given pod is running within the k8s cluster.
func (cs ConsoleManager) doInteractConsole(w http.ResponseWriter, r *http.Request) {
	// This is accessed with a connection that can be upgraded to a websocket. It was tested
	// using 'websocat' as 'curl' wasn't sufficient:
	// websocat -H "Authorization: Bearer ${TOKEN}" --request-uri https://api_gw_service.local wss://api_gw_service.local/apis/console-operator/console-operator/v1/interact/xname

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

	//log.Printf("WEBSOCKET:: creating executor")
	config, err := rest.InClusterConfig()
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		log.Printf("failed to create executor: %v", err)
	}

	// object to connect websocket streams to executor io
	webIO := &IOStreamer{conn: conn}

	log.Printf("WEBSOCKET:: starting command stream")
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
	log.Printf("WEBSOCKET:: Shutting down connection")
	conn.Close()

	log.Printf("WEBSOCKET:: Exiting websocket")
	//SendResponseJSON(w, http.StatusOK, nil)
}

// Finds and returns the node where the given pod is running within the k8s cluster.
func (cs ConsoleManager) doFollowConsole(w http.ResponseWriter, r *http.Request) {
	// This is accessed with a connection that can be upgraded to a websocket. It was tested
	// using 'websocat' as 'curl' wasn't sufficient:
	// websocat -H "Authorization: Bearer ${TOKEN}" --request-uri https://api_gw_service.local wss://api_gw_service.local/apis/console-operator/console-operator/v1/interact/xname

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
	cmd := []string{"conman", "-m", xname}

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

	//log.Printf("WEBSOCKET:: creating executor")
	config, err := rest.InClusterConfig()
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		log.Printf("failed to create executor: %v", err)
	}

	// object to connect websocket streams to executor io
	webIO := &IOStreamer{conn: conn}

	log.Printf("WEBSOCKET:: starting command stream")
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
	log.Printf("WEBSOCKET:: Shutting down connection")
	conn.Close()

	log.Printf("WEBSOCKET:: Exiting websocket")
	//SendResponseJSON(w, http.StatusOK, nil)
}

/*
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
	//defer func() {
	//	log.Printf("WEBSOCKET:: Doing deferred close")
	//}()

	// Build the command to be executed in the pod
	cmd := []string{"conman", "-m", xname}

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

	// object to connect websocket streams to executor io
	webIO := &IOStreamer{conn: conn}

	log.Printf("WEBSOCKET:: starting command stream")
	err = executor.Stream(remotecommand.StreamOptions{
		Stdin:  webIO,
		Stdout: webIO,
		Stderr: webIO,
		Tty:    false,
	})
	if err != nil {
		log.Printf("WEBSOCKET:: failed to execute command in pod: %v", err)
		//cancel()
		return
	}

	log.Printf("WEBSOCKET:: completed command stream")
	conn.Close()

	// 200 ok
	//SendResponseJSON(w, http.StatusOK, nil)
}
*/
