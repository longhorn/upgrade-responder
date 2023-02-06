package upgraderesponder

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	influxcli "github.com/influxdata/influxdb/client/v2"
	"sync"
	"time"
)

const maxSyncRetries = 5

type DBCache struct {
	sync.RWMutex
	Database     string
	Precision    string
	SyncInterval time.Duration
	CacheSize    int
	BatchPoints  influxcli.BatchPoints
	InfluxClient influxcli.Client
	syncChan     chan struct{}
}

func NewDBCache(database, precision string, syncInterval time.Duration, cacheSize int, influxClient influxcli.Client) (*DBCache, error) {
	bp, err := influxcli.NewBatchPoints(influxcli.BatchPointsConfig{
		Database:  database,
		Precision: precision,
	})
	if err != nil {
		return nil, err
	}

	dbCache := &DBCache{
		Database:     database,
		Precision:    precision,
		SyncInterval: syncInterval,
		CacheSize:    cacheSize,
		BatchPoints:  bp,
		InfluxClient: influxClient,
		syncChan:     make(chan struct{}),
	}

	return dbCache, nil
}

func (c *DBCache) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(c.SyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.Sync()
		case <-c.syncChan:
			c.Sync()
		case <-stop:
			return
		}
	}
}

func (c *DBCache) Sync() {
	c.Lock()
	defer c.Unlock()

	for i := 0; i < maxSyncRetries; i++ {
		err := c.InfluxClient.Write(c.BatchPoints)
		if err == nil {
			break
		} else if i < maxSyncRetries {
			logrus.Debugf("Failed to write %v points to database: %v. Retrying", len(c.BatchPoints.Points()), err)
		} else {
			logrus.Debugf("Failed to write %v points to database: %v. Dropped the batch points", len(c.BatchPoints.Points()), err)
		}
	}

	logrus.Debugf("synced %v points to database", len(c.BatchPoints.Points()))

	bp, err := influxcli.NewBatchPoints(influxcli.BatchPointsConfig{
		Database:  c.Database,
		Precision: c.Precision,
	})
	if err != nil {
		panic(fmt.Sprintf("Failed to create new batch points after sync: %v. Crashing the program. Please check the hard-coded parameters", err))
	}
	c.BatchPoints = bp
	return
}

func (c *DBCache) AddPoint(p *influxcli.Point) {
	c.Lock()
	defer c.Unlock()

	c.BatchPoints.AddPoint(p)
	if len(c.BatchPoints.Points()) >= c.CacheSize {
		c.syncChan <- struct{}{}
	}
	return
}
