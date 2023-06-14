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

	InfluxDBContinuousQueryFmt  = "cq_by_%s_down_sampling"
	InfluxDBMeasurementCountFmt = "by_%s_count_down_sampling"
	InfluxDBMeasurementMeanFmt  = "by_%s_mean_down_sampling"

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
	ValueFieldKey           = "value" // A dummy InfluxDB field used to count the number of points
	ValueFieldValue         = 1
)

type Server struct {
	done           chan struct{}
	VersionMap     map[string]*Version
	TagVersionsMap map[string][]*Version
	influxClient   influxcli.Client
	db             *maxminddb.Reader
	dbCache        *DBCache
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
	Name                 string // must be in semantic versioning
	ReleaseDate          string
	MinUpgradableVersion string // can be empty or semantic versioning
	Tags                 []string
	ExtraInfo            map[string]string
}

type CheckUpgradeRequest struct {
	AppVersion string `json:"appVersion"`

	ExtraTagInfo   map[string]string      `json:"extraTagInfo"`
	ExtraFieldInfo map[string]interface{} `json:"extraFieldInfo"`

	// Deprecated: replaced by ExtraTagInfo
	ExtraInfo map[string]string `json:"extraInfo"`
}

type CheckUpgradeResponse struct {
	Versions                 []Version `json:"versions"`
	RequestIntervalInMinutes int       `json:"requestIntervalInMinutes"`
}

func NewServer(done chan struct{}, applicationName, configFile, influxURL, influxUser, influxPass, queryPeriod, geodb string, cacheSyncInterval, cacheSize int) (*Server, error) {
	InfluxDBDatabase = applicationName + "_" + InfluxDBDatabase
	InfluxDBContinuousQueryPeriod = queryPeriod

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
		done:           done,
		VersionMap:     map[string]*Version{},
		TagVersionsMap: map[string][]*Version{},
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

	dbCache, err := NewDBCache(InfluxDBDatabase, InfluxDBPrecisionNanosecond, time.Duration(cacheSyncInterval)*time.Second, cacheSize, s.influxClient)
	if err != nil {
		return nil, err
	}
	s.dbCache = dbCache
	go s.dbCache.Run(done)

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
		InfluxDBContinuousQueryDownSampling, dbName, utils.ToSnakeCase(ValueFieldKey), InfluxDBMeasurementDownSampling, InfluxDBMeasurement, InfluxDBContinuousQueryPeriod)
	queryStrings[InfluxDBContinuousQueryByAppVersion] = fmt.Sprintf("CREATE CONTINUOUS QUERY %v ON %v BEGIN SELECT count(%v) as total INTO %v FROM %v GROUP BY time(%v),%v END",
		InfluxDBContinuousQueryByAppVersion, dbName, utils.ToSnakeCase(ValueFieldKey), InfluxDBMeasurementByAppVersion, InfluxDBMeasurement, InfluxDBContinuousQueryPeriod, InfluxDBTagAppVersion)
	queryStrings[InfluxDBContinuousQueryByCountryCode] = fmt.Sprintf("CREATE CONTINUOUS QUERY %v ON %v BEGIN SELECT count(%v) as total INTO %v FROM %v GROUP BY time(%v),%v END",
		InfluxDBContinuousQueryByCountryCode, dbName, utils.ToSnakeCase(ValueFieldKey), InfluxDBMeasurementByCountryCode, InfluxDBMeasurement, InfluxDBContinuousQueryPeriod, InfluxDBTagLocationCountryISOCode)

	s.addContinuousQuerieStringsFromTags(dbName, queryStrings)
	s.addContinuousQuerieStringsFromFields(dbName, queryStrings)

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

func (s *Server) addContinuousQuerieStringsFromFields(databaseName string, queryStrings map[string]string) error {
	influxQuery := influxcli.NewQuery(fmt.Sprintf("SHOW FIELD KEYS FROM %s", InfluxDBMeasurement), databaseName, "")
	influxResp, err := s.influxClient.Query(influxQuery)
	if err != nil {
		return errors.Wrapf(err, "failed to get all field keys from %s", InfluxDBMeasurement)
	}

	keyDataTypeMap := s.getKeyDataTypeMapFromQueryResponse(*influxResp, nil)
	for keyCamel, dataType := range keyDataTypeMap {
		key := utils.ToSnakeCase(keyCamel)
		continuousQuery := fmt.Sprintf(InfluxDBContinuousQueryFmt, key)

		var measurement string
		switch dataType {
		case "float":
			measurement = fmt.Sprintf(InfluxDBMeasurementMeanFmt, key)
			queryStrings[continuousQuery] = fmt.Sprintf("CREATE CONTINUOUS QUERY %v ON %v BEGIN SELECT MEAN(%v) AS total INTO %v FROM %v GROUP BY time(%v) END",
				continuousQuery, databaseName, key, measurement, InfluxDBMeasurement, InfluxDBContinuousQueryPeriod)
		case "boolean":
			measurement = fmt.Sprintf(InfluxDBMeasurementCountFmt, key)
			queryStrings[continuousQuery] = fmt.Sprintf("CREATE CONTINUOUS QUERY %v ON %v BEGIN SELECT COUNT(%v) AS total INTO %v FROM %v WHERE %v = true GROUP BY time(%v) END",
				continuousQuery, databaseName, key, measurement, InfluxDBMeasurement, key, InfluxDBContinuousQueryPeriod)
		default:
			measurement = fmt.Sprintf(InfluxDBMeasurementCountFmt, key)
			queryStrings[continuousQuery] = fmt.Sprintf("CREATE CONTINUOUS QUERY %v ON %v BEGIN SELECT COUNT(%v) AS total INTO %v FROM %v GROUP BY time(%v) END",
				continuousQuery, databaseName, key, measurement, InfluxDBMeasurement, InfluxDBContinuousQueryPeriod)
		}
	}
	return nil
}

func (s *Server) addContinuousQuerieStringsFromTags(databaseName string, queryStrings map[string]string) error {
	influxQuery := influxcli.NewQuery(fmt.Sprintf("SHOW TAG KEYS FROM %s", InfluxDBMeasurement), databaseName, "")
	influxResp, err := s.influxClient.Query(influxQuery)
	if err != nil {
		return errors.Wrapf(err, "failed to get all tag keys from %s", InfluxDBMeasurement)
	}

	excludeTags := map[string]bool{
		InfluxDBTagAppVersion:             true,
		InfluxDBTagLocationCountryISOCode: true,
	}
	tagKeys := s.getKeyDataTypeMapFromQueryResponse(*influxResp, excludeTags)
	for tagCamel := range tagKeys {
		tag := utils.ToSnakeCase(tagCamel)
		continuousQuery := fmt.Sprintf(InfluxDBContinuousQueryFmt, tag)
		measurement := fmt.Sprintf(InfluxDBMeasurementCountFmt, tag)
		queryStrings[continuousQuery] = fmt.Sprintf("CREATE CONTINUOUS QUERY %v ON %v BEGIN SELECT count(%v) as total INTO %v FROM %v GROUP BY time(%v),%v END",
			continuousQuery, databaseName, utils.ToSnakeCase(ValueFieldKey), measurement, InfluxDBMeasurement, InfluxDBContinuousQueryPeriod, tag)
	}
	return nil
}

func (s *Server) getKeyDataTypeMapFromQueryResponse(influxResp influxcli.Response, excludeKeys map[string]bool) (keys map[string]string) {
	keys = map[string]string{}
	for _, result := range influxResp.Results {
		for _, series := range result.Series {
			for _, value := range series.Values {
				key := value[0].(string)
				if excludeKeys[key] {
					continue
				}

				dataType := "unsigned"
				switch len(value) {
				case 1:
				default:
					dataType = value[1].(string)
				}
				keys[key] = dataType
			}
		}
	}
	return keys
}

func (s *Server) validateAndLoadResponseConfig(config *ResponseConfig) error {
	for i, v := range config.Versions {
		if len(v.Tags) == 0 {
			return fmt.Errorf("invalid empty label for %v", v)
		}
		if s.VersionMap[v.Name] != nil {
			return fmt.Errorf("invalid duplicate name %v", v.Name)
		}
		if _, err := semver.NewVersion(v.Name); err != nil {
			return err
		}
		if v.MinUpgradableVersion != "" {
			if _, err := semver.NewVersion(v.MinUpgradableVersion); err != nil {
				return err
			}
		}
		if _, err := ParseTime(v.ReleaseDate); err != nil {
			return err
		}
		for _, l := range v.Tags {
			s.TagVersionsMap[l] = append(s.TagVersionsMap[l], &config.Versions[i])
		}
		s.VersionMap[v.Name] = &config.Versions[i]
	}
	if len(s.TagVersionsMap[VersionTagLatest]) == 0 {
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
	//_, version, err := s.getParsedVersionWithTag(VersionTagLatest)
	//if err != nil {
	//	logrus.Errorf("BUG: unable to get an valid tag for %v: %v", VersionTagLatest, err)
	//	return nil, err
	//}
	/* disable version dependency reseponse
	if reqVer.LessThan(latestVer) {
		resp.Versions = append(resp.Versions, *version)
	}
	*/
	//resp.Versions = append(resp.Versions, *version)

	for _, v := range s.VersionMap {
		resp.Versions = append(resp.Versions, *v)
	}

	d, err := time.ParseDuration(InfluxDBContinuousQueryPeriod)
	if err != nil {
		logrus.Errorf("fail to parse InfluxDBContinuousQueryPeriod while building upgrade response: %v", err)
		resp.RequestIntervalInMinutes = 60
	} else {
		resp.RequestIntervalInMinutes = int(d.Minutes())
	}

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

	// We use IP to find the location but we don't store IP
	location, err := s.getLocation(publicIP)
	if err != nil {
		logrus.Error("Failed to get location for one ip")
	}

	if s.influxClient != nil {
		var err error
		defer func() {
			if err != nil {
				logrus.Errorf("Failed to recordRequest: %v", err)
			}
		}()

		s.recordTagsFromRequest(req, location)
		s.recordFieldsFromRequest(req)
	}
}

func (s *Server) recordTagsFromRequest(req *CheckUpgradeRequest, location *Location) {
	tags := map[string]string{
		InfluxDBTagAppVersion: req.AppVersion,
	}
	extraTagInfo := utils.MergeStringMaps(req.ExtraInfo, req.ExtraTagInfo)
	for k, v := range extraTagInfo {
		tags[utils.ToSnakeCase(k)] = v
	}

	if location != nil {
		tags[InfluxDBTagLocationCity] = location.City
		tags[InfluxDBTagLocationCountry] = location.Country.Name
		tags[InfluxDBTagLocationCountryISOCode] = location.Country.ISOCode
	}

	fields := map[string]interface{}{
		utils.ToSnakeCase(ValueFieldKey): ValueFieldValue,
	}

	pt, err := influxcli.NewPoint(InfluxDBMeasurement, tags, fields, time.Now())
	if err != nil {
		logrus.WithError(err).Error("Failed to record tags from request")
		return
	}

	s.dbCache.AddPoint(pt)
}

func (s *Server) recordFieldsFromRequest(req *CheckUpgradeRequest) {
	fields := make(map[string]interface{}, len(req.ExtraFieldInfo))
	for k, v := range req.ExtraFieldInfo {
		fields[utils.ToSnakeCase(k)] = v
	}

	pt, err := influxcli.NewPoint(InfluxDBMeasurement, nil, fields, time.Now())
	if err != nil {
		logrus.WithError(err).Error("Failed to record fields from request")
		return
	}

	s.dbCache.AddPoint(pt)
}
