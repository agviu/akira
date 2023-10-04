package main

import (
	"agviu/akira/source/docker"
	"fmt"
)

func main() {
	// fmt.Printf("Docker SDK for Go version: %s\n", client.DefaultVersion)
	// Set the Docker API version to 1.41
	// client.DefaultVersion = "1.41"
	// Create a Docker client object using the default options and environment variables
	if false {
		docker.CollectStats()
	}
	ch := make(chan string) // create an integer channel

	// start a Goroutine to send data to the channel
	go func() {
		ch <- "something"
		ch <- "wicked"   // send 10 to the channel
		ch <- "this day" // send 20 to the channel
		ch <- "comes"    // send 30 to the channel
		close(ch)        // close the channel
	}()

	// iterate over the values received from the channel
	for x := range ch {
		// x := <-ch
		fmt.Println(x) // print the received value
		// x = <-ch
		// fmt.Println(x)
		// x = <-ch
		// fmt.Println(x)
	}

	// var a []int
	var c, c1, c2, c3 chan int
	var i1, i2 int
	select {
	case i1 = <-c1:
		print("received ", i1, " from c1\n")
	case c2 <- i2:
		print("sent ", i2, " to c2\n")
	case i3, ok := (<-c3): // same as: i3, ok := <-c3
		if ok {
			print("received ", i3, " from c3\n")
		} else {
			print("c3 is closed\n")
		}
	// case a[f()] = <-c4:
	// same as:
	// case t := <-c4
	//	a[f()] = t
	default:
		print("no communication\n")
	}

	done := make(chan bool)
	go func() {
		for {
			select {
			case val := <-c:
				// process val
				print("received ", val, " from c\n")
			case <-done:
				return
			}
		}
	}()

	for { // send random sequence of bits to c
		select {
		case c <- 0: // note: no statement, no fallthrough, no folding of cases
		case c <- 1:
		}
	}
	done <- true
	select {} // block fo

}
