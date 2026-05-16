package store

import (
	"github.com/org/ch-api/pkg/logging"
	"github.com/org/ch-api/pkg/metrics"
)

// Store is the persistence layer that holds VM state and operation logs.
type Store struct {
	logger         logging.Logger
	VMs            *VMStore
	VMOperationLog *OperationLog
}

// New creates a Store with an empty VM store and operation log.
func New(logger logging.Logger, mr *metrics.Registry) *Store {
	return &Store{
		logger:         logger,
		VMs:            NewVMStore(mr),
		VMOperationLog: mustOperationLog("data/vm-operations.log"),
	}
}

func mustOperationLog(path string) *OperationLog {
	l, err := NewOperationLog(path)
	if err != nil {
		panic(err)
	}
	return l
}