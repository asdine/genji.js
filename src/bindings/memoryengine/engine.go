package memoryengine

import (
	"context"
	"errors"

	"github.com/genjidb/genji/engine"
)

// Engine is a simple memory engine implementation that stores data in
// an in-memory Btree. It is not thread safe.
type Engine struct {
	closed    bool
	stores    map[string]*sortedList
	sequences map[string]uint64
}

// NewEngine creates an in-memory engine.
func NewEngine() *Engine {
	return &Engine{
		stores:    make(map[string]*sortedList),
		sequences: make(map[string]uint64),
	}
}

// Begin creates a transaction.
func (ng *Engine) Begin(ctx context.Context, opts engine.TxOptions) (engine.Transaction, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if ng.closed {
		return nil, errors.New("engine closed")
	}

	return &transaction{ctx: ctx, ng: ng, writable: opts.Writable}, nil
}

// Close the engine.
func (ng *Engine) Close() error {
	if ng.closed {
		return errors.New("engine already closed")
	}

	ng.closed = true
	return nil
}

// This implements the engine.Transaction type.
type transaction struct {
	ctx        context.Context
	ng         *Engine
	writable   bool
	onRollback []func() // called during a rollback
	onCommit   []func() // called during a commit
	terminated bool
}

// If the transaction is writable, rollback calls
// every function stored in the onRollback slice
// to undo every mutation done since the beginning
// of the transaction.
func (tx *transaction) Rollback() error {
	if tx.terminated {
		return engine.ErrTransactionDiscarded
	}

	tx.terminated = true

	if tx.writable {
		for _, undo := range tx.onRollback {
			undo()
		}
	}

	select {
	case <-tx.ctx.Done():
		return tx.ctx.Err()
	default:
	}

	return nil
}

// If the transaction is writable, Commit calls
// every function stored in the onCommit slice
// to finalize every mutation done since the beginning
// of the transaction.
func (tx *transaction) Commit() error {
	if tx.terminated {
		return engine.ErrTransactionDiscarded
	}

	if !tx.writable {
		return engine.ErrTransactionReadOnly
	}

	select {
	case <-tx.ctx.Done():
		return tx.Rollback()
	default:
	}

	tx.terminated = true

	for _, fn := range tx.onCommit {
		fn()
	}

	return nil
}

func (tx *transaction) GetStore(name []byte) (engine.Store, error) {
	select {
	case <-tx.ctx.Done():
		return nil, tx.ctx.Err()
	default:
	}

	sl, ok := tx.ng.stores[string(name)]
	if !ok {
		return nil, engine.ErrStoreNotFound
	}

	return &storeTx{ctx: tx.ctx, tx: tx, sl: sl, name: string(name)}, nil
}

func (tx *transaction) CreateStore(name []byte) error {
	select {
	case <-tx.ctx.Done():
		return tx.ctx.Err()
	default:
	}

	if !tx.writable {
		return engine.ErrTransactionReadOnly
	}

	_, err := tx.GetStore(name)
	if err == nil {
		return engine.ErrStoreAlreadyExists
	}

	sl := newSortedList()

	tx.ng.stores[string(name)] = sl

	// on rollback, remove the btree from the list of stores
	tx.onRollback = append(tx.onRollback, func() {
		delete(tx.ng.stores, string(name))
	})

	return nil
}

func (tx *transaction) DropStore(name []byte) error {
	select {
	case <-tx.ctx.Done():
		return tx.ctx.Err()
	default:
	}

	if !tx.writable {
		return engine.ErrTransactionReadOnly
	}

	rsl, ok := tx.ng.stores[string(name)]
	if !ok {
		return engine.ErrStoreNotFound
	}

	delete(tx.ng.stores, string(name))

	// on rollback put back the btree to the list of stores
	tx.onRollback = append(tx.onRollback, func() {
		tx.ng.stores[string(name)] = rsl
	})

	return nil
}
