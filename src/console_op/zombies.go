// Copyright 2021 Hewlett Packard Enterprise Development LP

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
