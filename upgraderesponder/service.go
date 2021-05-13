package upgraderesponder

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Masterminds/semver"
	"github.com/Sirupsen/logrus"
	influxcli "github.com/influxdata/influxdb/client/v2"
	maxminddb "github.com/oschwald/maxminddb-golang"
	"github.com/pkg/errors"

	"github.com/longhorn/upgrade-responder/utils"
)

const (
	VersionTagLatest  = "latest"
	AppMinimalVersion = "v0.0.1"

	InfluxDBMeasurement              = "upgrade_request"
	InfluxDBMeasurementDownSampling  = "upgrade_request_down_sampling"
	InfluxDBMeasurementByAppVersion  = "by_app_version_down_sampling"
	InfluxDBMeasurementByCountryCode = "by_country_code_down_sampling"

	InfluxDBContinuousQueryDownSampling  = "cq_upgrade_request_down_sampling"
	InfluxDBContinuousQueryByAppVersion  = "cq_by_app_version_down_sampling"
	InfluxDBContinuousQueryByCountryCode = "cq_by_country_code_down_sampling"
)

var (
	InfluxDBPrecisionNanosecond   = "ns" // ns is good for counting nodes
	InfluxDBDatabase              = "upgrade_responder"
	InfluxDBContinuousQueryPeriod = "1h"

	InfluxDBTagAppVersion             = "app_version"
	InfluxDBTagKubernetesVersion      = "kubernetes_version"
	InfluxDBTagLocationCity           = "city"
	InfluxDBTagLocationCountry        = "country"
	InfluxDBTagLocationCountryISOCode = "country_isocode"

	HTTPHeaderXForwardedFor = "X-Forwarded-For"
	HTTPHeaderRequestID     = "X-Request-Id"
)

type Server struct {
	done          chan struct{}
	VersionMap    map[string]*Version
	TagVersionMap map[string]*Version
	influxClient  influxcli.Client
	db            *maxminddb.Reader
}

type Location struct {
	City    string `json:"city"`
	Country struct {
		Name    string
		ISOCode string
	} `json:"country"`
}

type ResponseConfig struct {
	Versions []Version
}

type Version struct {
	Name        string // must be in semantic versioning
	ReleaseDate string
	Tags        []string
}

type CheckUpgradeRequest struct {
	AppVersion string            `json:"appVersion"`
	ExtraInfo  map[string]string `json:"extraInfo"`
}

type CheckUpgradeResponse struct {
	Versions []Version `json:"versions"`
}

func NewServer(done chan struct{}, applicationName, configFile, influxURL, influxUser, influxPass, queryPeriod, geodb string) (*Server, error) {
	InfluxDBDatabase = applicationName + "_" + InfluxDBDatabase
	if queryPeriod != "" {
		InfluxDBContinuousQueryPeriod = queryPeriod
	}

	path := filepath.Clean(configFile)
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrap(err, "fail to open configFile")
	}
	defer f.Close()

	var config ResponseConfig
	if err := json.NewDecoder(f).Decode(&config); err != nil {
		return nil, err
	}
	s := &Server{
		done:          done,
		VersionMap:    map[string]*Version{},
		TagVersionMap: map[string]*Version{},
	}
	if err := s.validateAndLoadResponseConfig(&config); err != nil {
		return nil, err
	}

	db, err := maxminddb.Open(geodb)
	if err != nil {
		return nil, errors.Wrap(err, "fail to open geodb file")
	}
	s.db = db
	logrus.Debugf("GeoDB opened")

	if influxURL != "" {
		cfg := influxcli.HTTPConfig{
			Addr:               influxURL,
			InsecureSkipVerify: true,
		}
		if influxUser != "" {
			cfg.Username = influxUser
		}
		if influxPass != "" {
			cfg.Password = influxPass
		}
		c, err := influxcli.NewHTTPClient(cfg)
		if err != nil {
			return nil, err
		}
		logrus.Debugf("InfluxDB connection established")

		s.influxClient = c
		if err := s.initDB(); err != nil {
			return nil, err
		}
	}
	go func() {
		<-done
		if err := s.db.Close(); err != nil {
			logrus.Debugf("Failed to close geodb: %v", err)
		} else {
			logrus.Debugf("Geodb connection closed")
		}
		if s.influxClient != nil {
			if err := s.influxClient.Close(); err != nil {
				logrus.Debugf("Failed to close InfluxDB connection: %v", err)
			} else {
				logrus.Debug("InfluxDB connection closed")
			}
		}
	}()
	return s, nil
}

func (s *Server) initDB() error {
	if err := s.createDB(InfluxDBDatabase); err != nil {
		return err
	}
	if err := s.createContinuousQueries(InfluxDBDatabase); err != nil {
		return err
	}
	return nil
}

func (s *Server) createDB(name string) error {
	q := influxcli.NewQuery("CREATE DATABASE "+name, "", "")
	response, err := s.influxClient.Query(q)
	if err != nil {
		return err
	}
	if response.Error() != nil {
		return response.Error()
	}
	logrus.Debugf("Database %v is either created or already exists", name)
	return nil
}

func (s *Server) createContinuousQueries(dbName string) error {
	queryStrings := map[string]string{}

	queryStrings[InfluxDBContinuousQueryDownSampling] = fmt.Sprintf("CREATE CONTINUOUS QUERY %v ON %v BEGIN SELECT count(%v) as total INTO %v FROM %v GROUP BY time(%v) END",
		InfluxDBContinuousQueryDownSampling, dbName, utils.ToSnakeCase(HTTPHeaderRequestID), InfluxDBMeasurementDownSampling, InfluxDBMeasurement, InfluxDBContinuousQueryPeriod)
	queryStrings[InfluxDBContinuousQueryByAppVersion] = fmt.Sprintf("CREATE CONTINUOUS QUERY %v ON %v BEGIN SELECT count(%v) as total INTO %v FROM %v GROUP BY time(%v),%v END",
		InfluxDBContinuousQueryByAppVersion, dbName, utils.ToSnakeCase(HTTPHeaderRequestID), InfluxDBMeasurementByAppVersion, InfluxDBMeasurement, InfluxDBContinuousQueryPeriod, InfluxDBTagAppVersion)
	queryStrings[InfluxDBContinuousQueryByCountryCode] = fmt.Sprintf("CREATE CONTINUOUS QUERY %v ON %v BEGIN SELECT count(%v) as total INTO %v FROM %v GROUP BY time(%v),%v END",
		InfluxDBContinuousQueryByCountryCode, dbName, utils.ToSnakeCase(HTTPHeaderRequestID), InfluxDBMeasurementByCountryCode, InfluxDBMeasurement, InfluxDBContinuousQueryPeriod, InfluxDBTagLocationCountryISOCode)

	for queryName, queryString := range queryStrings {
		query := influxcli.NewQuery(queryString, "", "")
		response, err := s.influxClient.Query(query)
		if err != nil {
			return err
		}
		if err := response.Error(); err != nil {
			if utils.IsAlreadyExistsError(err) {
				logrus.Debugf("The continuous query %v is already exists and cannot be modified. If you modified --query-period, please manually drop the continuous query %v from the database %v and retry", queryName, queryName, dbName)
			}
			return err
		}
		logrus.Debugf("Created continuous query %v", queryName)
	}
	return nil
}

func (s *Server) validateAndLoadResponseConfig(config *ResponseConfig) error {
	for _, v := range config.Versions {
		if len(v.Tags) == 0 {
			return fmt.Errorf("invalid empty label for %v", v)
		}
		if s.VersionMap[v.Name] != nil {
			return fmt.Errorf("invalid duplicate name %v", v.Name)
		}
		if _, err := semver.NewVersion(v.Name); err != nil {
			return err
		}
		if _, err := ParseTime(v.ReleaseDate); err != nil {
			return err
		}
		for _, l := range v.Tags {
			if s.TagVersionMap[l] != nil {
				return fmt.Errorf("invalid duplicate label %v", l)
			}
			s.TagVersionMap[l] = &v
		}
		s.VersionMap[v.Name] = &v
	}
	if s.TagVersionMap[VersionTagLatest] == nil {
		return fmt.Errorf("no latest label specified")
	}
	return nil
}

func (s *Server) HealthCheck(rw http.ResponseWriter, req *http.Request) {
	rw.WriteHeader(http.StatusOK)
}

func (s *Server) CheckUpgrade(rw http.ResponseWriter, req *http.Request) {
	var (
		err       error
		checkReq  CheckUpgradeRequest
		checkResp *CheckUpgradeResponse
	)

	defer func() {
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
		}
	}()

	if err = json.NewDecoder(req.Body).Decode(&checkReq); err != nil {
		return
	}
	logrus.Debugf("Request %v", &checkReq)

	s.recordRequest(req, &checkReq)

	checkResp, err = s.GenerateCheckUpgradeResponse(&checkReq)
	if err != nil {
		logrus.Errorf("Failed to GenerateCheckUpgradeResponse: %v", err)
		return
	}

	if err = respondWithJSON(rw, checkResp); err != nil {
		logrus.Errorf("Failed to repsondWithJSON: %v", err)
		return
	}
	return
}

func respondWithJSON(rw http.ResponseWriter, obj interface{}) error {
	response, err := json.Marshal(obj)
	if err != nil {
		return errors.Wrapf(err, "fail to marshal %v", obj)
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	_, err = rw.Write(response)
	return err
}

func (s *Server) getParsedVersionWithTag(tag string) (*semver.Version, *Version, error) {
	v, exists := s.TagVersionMap[tag]
	if !exists {
		return nil, nil, fmt.Errorf("cannot find version with tag %v", tag)
	}
	ver, err := semver.NewVersion(v.Name)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "version %v is not valid with tag %v", v.Name, tag)
	}
	return ver, v, nil
}

func (s *Server) GenerateCheckUpgradeResponse(request *CheckUpgradeRequest) (*CheckUpgradeResponse, error) {
	/* disable version dependency reseponse
	reqVer, err := semver.NewVersion(request.AppVersion)
	if err != nil {
		logrus.Warnf("Invalid version in request: %v: %v, response with the latest version", request.AppVersion, err)
		reqVer, err = semver.NewVersion(AppMinimalVersion)
		if err != nil {
			return nil, err
		}
	}
	*/
	resp := &CheckUpgradeResponse{}

	// Only supports `latest` label for now
	//latestVer, version, err := s.getParsedVersionWithTag(VersionTagLatest)
	_, version, err := s.getParsedVersionWithTag(VersionTagLatest)
	if err != nil {
		logrus.Errorf("BUG: unable to get an valid tag for %v: %v", VersionTagLatest, err)
		return nil, err
	}
	/* disable version dependency reseponse
	if reqVer.LessThan(latestVer) {
		resp.Versions = append(resp.Versions, *version)
	}
	*/
	resp.Versions = append(resp.Versions, *version)
	return resp, nil
}

func ParseTime(t string) (time.Time, error) {
	return time.Parse(time.RFC3339, t)
}

type locationRecord struct {
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Country struct {
		Names   map[string]string `maxminddb:"names"`
		ISOCode string            `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

func (s *Server) getLocation(addr string) (*Location, error) {
	var (
		record locationRecord
		loc    Location
	)
	ip := net.ParseIP(addr)

	err := s.db.Lookup(ip, &record)
	if err != nil {
		return nil, err
	}

	loc.City = record.City.Names["en"]
	loc.Country.Name = record.Country.Names["en"]
	loc.Country.ISOCode = record.Country.ISOCode
	return &loc, nil
}

//func canonializeField(name string) string {
//	return strings.Replace(strings.ToLower(HTTPHeaderRequestID), "-", "_", -1)
//}

// Don't need to return error to the requester
func (s *Server) recordRequest(httpReq *http.Request, req *CheckUpgradeRequest) {
	xForwaredFor := httpReq.Header[HTTPHeaderXForwardedFor]
	publicIP := ""
	l := len(xForwaredFor)
	if l > 0 {
		// rightmost IP must be the public IP
		publicIP = xForwaredFor[l-1]
	}

	xRequestId := httpReq.Header[HTTPHeaderRequestID]
	requestID := ""
	if len(xRequestId) > 0 {
		requestID = xRequestId[0]
	}

	// We use IP to find the location but we don't store IP
	loc, err := s.getLocation(publicIP)
	if err != nil {
		logrus.Error("Failed to get location for one ip")
	}
	logrus.Debugf("HTTP request: RequestID \"%v\", Location %+v, req %v",
		requestID, loc, req)

	if s.influxClient != nil {
		var (
			err error
			pt  *influxcli.Point
		)
		defer func() {
			if err != nil {
				logrus.Errorf("Failed to recordRequest: %v", err)
			}
		}()

		tags := map[string]string{
			InfluxDBTagAppVersion: req.AppVersion,
		}
		for k, v := range req.ExtraInfo {
			tags[utils.ToSnakeCase(k)] = v
		}
		fields := map[string]interface{}{
			utils.ToSnakeCase(HTTPHeaderRequestID): requestID,
		}
		if loc != nil {
			tags[InfluxDBTagLocationCity] = loc.City
			tags[InfluxDBTagLocationCountry] = loc.Country.Name
			tags[InfluxDBTagLocationCountryISOCode] = loc.Country.ISOCode
		}
		pt, err = influxcli.NewPoint(InfluxDBMeasurement, tags, fields, time.Now())
		if err != nil {
			return
		}

		if err = s.addPoint(pt, InfluxDBDatabase, InfluxDBPrecisionNanosecond); err != nil {
			return
		}
	}
}

func (s *Server) addPoint(pt *influxcli.Point, db, precision string) error {
	bp, err := influxcli.NewBatchPoints(influxcli.BatchPointsConfig{
		Database:  db,
		Precision: precision,
	})
	if err != nil {
		return err
	}
	bp.AddPoint(pt)
	if err = s.influxClient.Write(bp); err != nil {
		return err
	}
	return nil
}
