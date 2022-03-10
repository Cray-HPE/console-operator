//
//  MIT License
//
//  (C) Copyright 2021-2022 Hewlett Packard Enterprise Development LP
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

// This file contains the code needed to handle zombie processes

package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// NOTE:  Any time a container is started with a particular application running
//  as nid0, that process is required to handle any zombie processes that are
//  orphaned in the pod.  This is a process running in the background that will
//  find zombie processes and terminate them cleanly.

// Function to scan the process table for zombie processes
func watchForZombies() {
	for {
		// get the process information from the system
		zombies := findZombies()
		// look for zombies and terminate them
		for _, zombie := range zombies {
			// kill each zombie in a separate thread
			go killZombie(zombie)
		}
		// wait for a bit before looking again
		time.Sleep(30 * time.Second)
	}
}

// Find all the current zombie processes
func findZombies() []int {
	var zombies []int = nil
	var outBuf bytes.Buffer
	// Use a 'ps -eo' style command as the basis to search for zombie processes
	// and put the output in outBuf.
	cmd := exec.Command("ps", "-eo", "pid,stat")
	cmd.Stderr = &outBuf
	cmd.Stdout = &outBuf
	err := cmd.Run()
	if err != nil {
		log.Printf("Error getting current processes: %s", err)
	}
	// process the output buffer to find zombies
	var readLine string
	for {
		// pull off a line of output and
		if readLine, err = outBuf.ReadString('\n'); err == io.EOF {
			break
		} else if err != nil {
			log.Printf("Error reading current process output: %s", err)
			break
		}
		// NOTE: a 'STATUS' of "Z" denotes a zombie process
		cols := strings.Fields(readLine)
		if len(cols) >= 2 && cols[1] == "Z" {
			// found a zombie
			zPid, err := strconv.Atoi(cols[0])
			if err == nil {
				log.Printf("Found a zombie process: %d", zPid)
				zombies = append(zombies, zPid)
			} else {
				// atoi did not like our process "number"
				log.Printf("Thought we had a zombie, couldn't get pid:%s", readLine)
			}
		}
	}
	return zombies
}

// Kill (wait for) the zombie process with the given pid
func killZombie(pid int) {
	log.Printf("Killing zombie process: %d", pid)
	p, err := os.FindProcess(pid)
	if err != nil {
		log.Printf("Error attaching to zombie process %d, err:%s", pid, err)
		return
	}
	// should just need to get the exit state to clean up process
	_, err = p.Wait()
	if err != nil {
		log.Printf("Error waiting for zombie process %d, err:%s", pid, err)
		return
	}
	log.Printf("Cleaned up zombie process: %d", pid)
}
