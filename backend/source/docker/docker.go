package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/docker/cli/cli/command"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
)

// "json"
type stats struct {
	mu sync.RWMutex
	cs []*Stats
}

// StatsEntry represents represents the statistics data collected from a container
type StatsEntry struct {
	Container        string
	Name             string
	ID               string
	CPUPercentage    float64
	Memory           float64 // On Windows this is the private working set
	MemoryLimit      float64 // Not used on Windows
	MemoryPercentage float64 // Not used on Windows
	NetworkRx        float64
	NetworkTx        float64
	BlockRead        float64
	BlockWrite       float64
	PidsCurrent      uint64 // Not used on Windows
	IsInvalid        bool
}

type Stats struct {
	mutex sync.RWMutex
	StatsEntry
	err error
}

func CollectStats() {
	closeChan := make(chan error)
	ctx := context.Background()

	monitorContainerEvents := func(started chan<- struct{}, c chan events.Message, stopped <-chan struct{}) {
		// Lets create the filters, we want only information about containers.
		f := filters.NewArgs()
		f.Add("type", "container")
		options := types.EventsOptions{
			Filters: f,
		}

		eventq, errq := dockerCli.Client().Events(ctx, options)

		// Whether we successfully subscribed to eventq or not, we can now
		// unblock the main goroutine.
		close(started)
		defer close(c)

		for {
			select {
			case <-stopped:
				return
			case event := <-eventq:
				c <- event
			case err := <-errq:
				closeChan <- err
				return
			}
		}
	}

	waitFirst := &sync.WaitGroup{}

	cStats := stats{}

	// Get the list of containers and create a goroutine for each of them.
	getContainerList := func() {
		options := types.ContainerListOptions{
			All: opts.all,
		}
		cs, err := dockerCli.Client().ContainerList(ctx, options)
		if err != nil {
			closeChan <- err
		}
		for _, container := range cs {
			s := NewStats(container.ID[:12])
			if cStats.add(s) {
				waitFirst.Add(1)
				go collect(ctx, s, dockerCli.Client(), !opts.noStream, waitFirst)
			}
		}
	}

	started := make(chan struct{})
	eh := command.InitEventHandler()
	eh.Handle("create", func(e events.Message) {
		if opts.all {
			s := NewStats(e.ID[:12])
			if cStats.add(s) {
				waitFirst.Add(1)
				go collect(ctx, s, dockerCli.Client(), !opts.noStream, waitFirst)
			}
		}
	})

	eh.Handle("start", func(e events.Message) {
		s := NewStats(e.ID[:12])
		if cStats.add(s) {
			waitFirst.Add(1)
			go collect(ctx, s, dockerCli.Client(), !opts.noStream, waitFirst)
		}
	})

	eh.Handle("die", func(e events.Message) {
		if !opts.all {
			cStats.remove(e.ID[:12])
		}
	})

	eventChan := make(chan events.Message)
	go eh.Watch(eventChan)
	stopped := make(chan struct{})
	go monitorContainerEvents(started, eventChan, stopped)
	defer close(stopped)
	<-started

	// Start a short-lived goroutine to retrieve the initial list of
	// containers.
	getContainerList()

	// make sure each container get at least one valid stat data
	waitFirst.Wait()

	var err error
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		cleanScreen()
		ccstats := []StatsEntry{}
		cStats.mu.RLock()
		for _, c := range cStats.cs {
			ccstats = append(ccstats, c.GetStatistics())
		}
		cStats.mu.RUnlock()
		if err = statsFormatWrite(statsCtx, ccstats, daemonOSType, !opts.noTrunc); err != nil {
			break
		}
		if len(cStats.cs) == 0 && !showAll {
			break
		}
		if opts.noStream {
			break
		}
		select {
		case err, ok := <-closeChan:
			if ok {
				if err != nil {
					// this is suppressing "unexpected EOF" in the cli when the
					// daemon restarts so it shutdowns cleanly
					if err == io.ErrUnexpectedEOF {
						return nil
					}
					return err
				}
			}
		default:
			// just skip
		}
	}
	return err
}

func collect(ctx context.Context, s *Stats, cli client.APIClient, streamStats bool, waitFirst *sync.WaitGroup) {
	logrus.Debugf("collecting stats for %s", s.Container)
	var (
		getFirst       bool
		previousCPU    uint64
		previousSystem uint64
		u              = make(chan error, 1)
	)

	defer func() {
		// if error happens and we get nothing of stats, release wait group whatever
		if !getFirst {
			getFirst = true
			waitFirst.Done()
		}
	}()

	response, err := cli.ContainerStats(ctx, s.Container, streamStats)
	if err != nil {
		s.SetError(err)
		return
	}
	defer response.Body.Close()

	dec := json.NewDecoder(response.Body)
	go func() {
		for {
			var (
				v                      *types.StatsJSON
				memPercent, cpuPercent float64
				blkRead, blkWrite      uint64 // Only used on Linux
				mem, memLimit          float64
				pidsStatsCurrent       uint64
			)

			if err := dec.Decode(&v); err != nil {
				dec = json.NewDecoder(io.MultiReader(dec.Buffered(), response.Body))
				u <- err
				if err == io.EOF {
					break
				}
				time.Sleep(100 * time.Millisecond)
				continue
			}

			previousCPU = v.PreCPUStats.CPUUsage.TotalUsage
			previousSystem = v.PreCPUStats.SystemUsage
			cpuPercent = calculateCPUPercentUnix(previousCPU, previousSystem, v)
			blkRead, blkWrite = calculateBlockIO(v.BlkioStats)
			mem = calculateMemUsageUnixNoCache(v.MemoryStats)
			memLimit = float64(v.MemoryStats.Limit)
			memPercent = calculateMemPercentUnixNoCache(memLimit, mem)
			pidsStatsCurrent = v.PidsStats.Current

			netRx, netTx := calculateNetwork(v.Networks)
			s.SetStatistics(StatsEntry{
				Name:             v.Name,
				ID:               v.ID,
				CPUPercentage:    cpuPercent,
				Memory:           mem,
				MemoryPercentage: memPercent,
				MemoryLimit:      memLimit,
				NetworkRx:        netRx,
				NetworkTx:        netTx,
				BlockRead:        float64(blkRead),
				BlockWrite:       float64(blkWrite),
				PidsCurrent:      pidsStatsCurrent,
			})
			u <- nil
			if !streamStats {
				return
			}
		}
	}()
	for {
		select {
		case <-time.After(2 * time.Second):
			// zero out the values if we have not received an update within
			// the specified duration.
			s.SetErrorAndReset(errors.New("timeout waiting for stats"))
			// if this is the first stat you get, release WaitGroup
			if !getFirst {
				getFirst = true
				waitFirst.Done()
			}
		case err := <-u:
			s.SetError(err)
			if err == io.EOF {
				break
			}
			if err != nil {
				continue
			}
			// if this is the first stat you get, release WaitGroup
			if !getFirst {
				getFirst = true
				waitFirst.Done()
			}
		}
		if !streamStats {
			return
		}
	}
}

func calculateCPUPercentUnix(previousCPU, previousSystem uint64, v *types.StatsJSON) float64 {
	var (
		cpuPercent = 0.0
		// calculate the change for the cpu usage of the container in between readings
		cpuDelta = float64(v.CPUStats.CPUUsage.TotalUsage) - float64(previousCPU)
		// calculate the change for the entire system between readings
		systemDelta = float64(v.CPUStats.SystemUsage) - float64(previousSystem)
		onlineCPUs  = float64(v.CPUStats.OnlineCPUs)
	)

	if onlineCPUs == 0.0 {
		onlineCPUs = float64(len(v.CPUStats.CPUUsage.PercpuUsage))
	}
	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent = (cpuDelta / systemDelta) * onlineCPUs * 100.0
	}
	return cpuPercent
}

func calculateBlockIO(blkio types.BlkioStats) (uint64, uint64) {
	var blkRead, blkWrite uint64
	for _, bioEntry := range blkio.IoServiceBytesRecursive {
		if len(bioEntry.Op) == 0 {
			continue
		}
		switch bioEntry.Op[0] {
		case 'r', 'R':
			blkRead = blkRead + bioEntry.Value
		case 'w', 'W':
			blkWrite = blkWrite + bioEntry.Value
		}
	}
	return blkRead, blkWrite
}

func calculateNetwork(network map[string]types.NetworkStats) (float64, float64) {
	var rx, tx float64

	for _, v := range network {
		rx += float64(v.RxBytes)
		tx += float64(v.TxBytes)
	}
	return rx, tx
}
