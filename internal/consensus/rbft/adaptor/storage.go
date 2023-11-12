package adaptor

import (
	"bytes"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rosedblabs/rosedb/v2"
)

var (
	storeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "axiom_ledger",
		Subsystem: "consensus",
		Name:      "storage_action",
		Buckets:   prometheus.ExponentialBuckets(0.0001, 2, 10),
	}, []string{"type"})
)

func init() {
	prometheus.MustRegister(storeDuration)
}

// StoreState stores a key,value pair to the database with the given namespace
func (a *RBFTAdaptor) StoreState(key string, value []byte) error {
	start := time.Now()
	err := a.store.Put([]byte(key), value)
	storeDuration.WithLabelValues("StoreState").Observe(time.Since(start).Seconds())
	return err
}

// DelState removes a key,value pair from the database with the given namespace
func (a *RBFTAdaptor) DelState(key string) error {
	start := time.Now()
	err := a.store.Delete([]byte(key))
	storeDuration.WithLabelValues("DelState").Observe(time.Since(start).Seconds())
	return err
}

// ReadState retrieves a value to a key from the database with the given namespace
func (a *RBFTAdaptor) ReadState(key string) ([]byte, error) {
	start := time.Now()
	b, err := a.store.Get([]byte(key))
	storeDuration.WithLabelValues("ReadState").Observe(time.Since(start).Seconds())
	if err != nil {
		if errors.Is(err, rosedb.ErrKeyNotFound) {
			return nil, errors.New("not found")
		}
		return nil, err
	}
	if b == nil {
		return nil, errors.New("not found")
	}
	return b, nil
}

// ReadStateSet retrieves all key-value pairs where the key starts with prefix from the database with the given namespace
func (a *RBFTAdaptor) ReadStateSet(prefix string) (map[string][]byte, error) {
	start := time.Now()
	prefixRaw := []byte(prefix)

	ret := make(map[string][]byte)
	a.store.Ascend(func(k []byte, v []byte) (bool, error) {
		if bytes.HasPrefix(k, prefixRaw) {
			ret[string(k)] = append([]byte(nil), v...)
		}
		return true, nil
	})
	storeDuration.WithLabelValues("ReadStateSet").Observe(time.Since(start).Seconds())
	if len(ret) == 0 {
		return nil, errors.New("not found")
	}
	return ret, nil
}
