package transfer

import (
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

// A directory can distribute two full one-leg windows across up to eight
// files. Relay files charge both legs; recovery validation occupies a slot and
// its request allowance before it opens a read stream.
const directoryRequestBudget uint32 = 128

func streamWindowRequests(plan Plan, size *uint64) uint32 {
	if plan.Durability == "" || size == nil || *size >= uint64(providerapi.MaxReadAheadBytes) {
		return providerapi.MaxSFTPWriteWindowRequests
	}
	return max(uint32(1), uint32((*size+streamPacketBytes-1)/streamPacketBytes)) //nolint:gosec // size is below the 2 MiB window.
}

func streamBufferBytes(plan Plan) uint32 {
	if plan.Durability != "" {
		return min(plan.BufferBytes, streamPacketBytes)
	}
	return plan.BufferBytes
}

func directoryRequestCost(plan Plan, entry domain.Entry) uint32 {
	if plan.Durability == "" {
		return 0
	} // legacy scheduling is bounded by file slots
	legs := uint32(0)
	if plan.SourceEndpoint.Kind == domain.EndpointSSH {
		legs++
	}
	if plan.DestinationEndpoint.Kind == domain.EndpointSSH {
		legs++
	}
	return max(uint32(1), legs) * streamWindowRequests(plan, entry.Fingerprint.Size)
}

func windowedExecutionResourceUsage(plan Plan) ResourceUsage {
	endpoints := make(map[domain.EndpointID]struct{}, 2)
	for _, endpoint := range []domain.Endpoint{plan.SourceEndpoint, plan.DestinationEndpoint} {
		if endpoint.Kind == domain.EndpointSSH {
			endpoints[endpoint.ID] = struct{}{}
		}
	}
	connections := uint32(len(endpoints)) //nolint:gosec // at most two endpoints.
	slots := uint32(1)
	requests := directoryRequestCost(plan, domain.Entry{Fingerprint: plan.Source.Fingerprint})
	validation := uint64(0)
	if plan.Source.Kind == domain.EntryDirectory {
		slots = uint32(directoryStreamWorkers(plan)) //nolint:gosec // in 1..8.
		requests = directoryRequestBudget
		validation = streamPacketBytes
	}
	if connections == 0 {
		requests = 0
	}
	return ResourceUsage{
		ActiveJobs: 1, Connections: connections, SSHProcesses: connections,
		FileDescriptors: 2*slots + 3*connections,
		// Per-file allowance includes coordinator/control and read/write pumps;
		// packet workers share the admitted request budget across both legs.
		Goroutines:  requests + 8*slots + 2,
		MemoryBytes: uint64(requests)*streamPacketBytes + uint64(slots)*(uint64(streamBufferBytes(plan))+2*streamPacketBytes) + validation,
	}
}

func (worker *Worker) streamReadOptions(plan Plan, size *uint64) providerapi.ReadStreamOptions {
	requests := min(streamWindowRequests(plan, plan.Source.Fingerprint.Size), streamWindowRequests(plan, size))
	options := providerapi.ReadStreamOptions{MaxBytes: requests * streamPacketBytes}
	if worker.scheduler != nil {
		options.MaxRequestBytes = worker.scheduler.QuantumBytes()
		if plan.Durability != "" {
			options.MaxBytes = requests * min(options.MaxRequestBytes, streamPacketBytes)
		}
	}
	return options
}
