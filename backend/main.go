package main

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

func main() {
	// fmt.Printf("Docker SDK for Go version: %s\n", client.DefaultVersion)
	// Set the Docker API version to 1.41
    // client.DefaultVersion = "1.41"
	// Create a Docker client object using the default options and environment variables
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	// Use the Docker client to list all containers
	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{})
	if err != nil {
		panic(err)
	}

	// Print the ID and name of each container
	for _, container := range containers {
		fmt.Printf("%s %s\n", container.ID[:12], container.Names[0])
	}
}
