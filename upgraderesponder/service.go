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

	influxClientTimeOut = 10 * time.Second
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

	extraInfoTypeTag   = "tag"
	extraInfoTypeField = "field"

	defaultMaxStringValueLength = 200
)

type Server struct {
	done           chan struct{}
	VersionMap     map[string]*Version
	TagVersionsMap map[string][]*Version
	influxClient   influxcli.Client
	db             *maxminddb.Reader
	dbCache        *DBCache
	RequestSchema  RequestSchema
}

type Location struct {
	City    string `json:"city"`
	Country struct {
		Name    string
		ISOCode string
	} `json:"country"`
}

type ResponseConfig struct {
	Versions []Version `json:"versions"`
}

type Version struct {
	Name                 string            `json:"name"` // must be in semantic versioning
	ReleaseDate          string            `json:"releaseDate"`
	MinUpgradableVersion string            `json:"minUpgradableVersion"` // can be empty or semantic versioning
	Tags                 []string          `json:"tags"`
	ExtraInfo            map[string]string `json:"extraInfo"`
}

type RequestSchema struct {
	AppVersionSchema     Schema            `json:"appVersionSchema"`
	ExtraTagInfoSchema   map[string]Schema `json:"extraTagInfoSchema"`
	ExtraFieldInfoSchema map[string]Schema `json:"extraFieldInfoSchema"`
}

type Schema struct {
	DataType string `json:"dataType"`
	MaxLen   int    `json:"maxLen"`
}

func (sc *Schema) Validate(value interface{}) (isValid bool) {
	defer func() {
		if !isValid {
			logrus.Debugf("validate failed: schema %+v, value %v", sc, value)
		}
	}()

	switch sc.DataType {
	case "string":
		v, ok := value.(string)
		if !ok {
			return false
		}
		maxLen := defaultMaxStringValueLength
		if sc.MaxLen > 0 {
			maxLen = sc.MaxLen
		}
		return len(v) <= maxLen
	case "float":
		if _, ok := value.(float64); !ok {
			return false
		}
		return true
	case "boolean":
		if _, ok := value.(bool); !ok {
			return false
		}
		return true
	}
	return false
}

func (s *Server) ValidateExtraInfo(key string, value interface{}, extraInfoType string) bool {
	switch extraInfoType {
	case extraInfoTypeTag:
		schema, ok := s.RequestSchema.ExtraTagInfoSchema[key]
		if !ok {
			return false
		}
		return schema.Validate(value)
	case extraInfoTypeField:
		schema, ok := s.RequestSchema.ExtraFieldInfoSchema[key]
		if !ok {
			return false
		}
		return schema.Validate(value)
	default:
		return false
	}
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

func NewServer(done chan struct{}, applicationName, responseConfigFilePath, requestSchemaFilePath, influxURL, influxUser, influxPass, queryPeriod, geodb string, cacheSyncInterval, cacheSize int) (*Server, error) {
	InfluxDBDatabase = applicationName + "_" + InfluxDBDatabase
	InfluxDBContinuousQueryPeriod = queryPeriod

	responseConfigFile, err := os.Open(filepath.Clean(responseConfigFilePath))
	if err != nil {
		return nil, errors.Wrapf(err, "fail to open responseConfigFile at %v", responseConfigFilePath)
	}
	defer responseConfigFile.Close()

	var config ResponseConfig
	if err := json.NewDecoder(responseConfigFile).Decode(&config); err != nil {
		return nil, err
	}

	requestSchemaFile, err := os.Open(filepath.Clean(requestSchemaFilePath))
	if err != nil {
		return nil, errors.Wrapf(err, "fail to open requestSchemaFile at %v", requestSchemaFilePath)
	}
	defer requestSchemaFile.Close()

	var requestSchema RequestSchema
	if err := json.NewDecoder(requestSchemaFile).Decode(&requestSchema); err != nil {
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
	if err := s.validateAndLoadRequestSchema(requestSchema); err != nil {
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
			Timeout:            influxClientTimeOut,
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

func (s *Server) validateAndLoadRequestSchema(requestSchema RequestSchema) error {
	if requestSchema.AppVersionSchema.DataType != "string" {
		return fmt.Errorf("AppVersionSchema must have string data type: %v", requestSchema.AppVersionSchema.DataType)
	}
	if requestSchema.AppVersionSchema.MaxLen < 0 {
		return fmt.Errorf("AppVersionSchema must have MaxLen >= 0")
	}

	for schemaName, schema := range requestSchema.ExtraFieldInfoSchema {
		switch schema.DataType {
		case "string":
			if schema.MaxLen < 0 {
				return fmt.Errorf("schema %v with data type string must have Maxlen >= 0", schemaName)
			}
		case "float", "boolean":
		default:
			return fmt.Errorf("field schema %v has invalid data type %v", schemaName, schema.DataType)
		}
	}

	for schemaName, schema := range requestSchema.ExtraTagInfoSchema {
		switch schema.DataType {
		case "string":
			if schema.MaxLen < 0 {
				return fmt.Errorf("schema %v of data type string must have Maxlen >= 0", schemaName)
			}
		default:
			return fmt.Errorf("tag schema %v must have string data type %v", schemaName, schema.DataType)
		}
	}

	s.RequestSchema = requestSchema
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

		if !s.RequestSchema.AppVersionSchema.Validate(req.AppVersion) {
			err = errors.Errorf("AppVersion %v is not valid according to schema %+v", req.AppVersion, s.RequestSchema.AppVersionSchema)
			return
		}

		tags := s.getTagsFromRequest(req, location)
		fields := s.getFieldsFromRequest(req)

		pt, err := influxcli.NewPoint(InfluxDBMeasurement, tags, fields, time.Now())
		if err != nil {
			return
		}
		s.dbCache.AddPoint(pt)
	}
}

func (s *Server) getTagsFromRequest(req *CheckUpgradeRequest, location *Location) map[string]string {
	tags := map[string]string{
		InfluxDBTagAppVersion: req.AppVersion,
	}
	extraTagInfo := utils.MergeStringMaps(req.ExtraInfo, req.ExtraTagInfo)
	for k, v := range extraTagInfo {
		if s.ValidateExtraInfo(k, v, extraInfoTypeTag) {
			tags[utils.ToSnakeCase(k)] = v
		}
	}

	if location != nil {
		tags[InfluxDBTagLocationCity] = location.City
		tags[InfluxDBTagLocationCountry] = location.Country.Name
		tags[InfluxDBTagLocationCountryISOCode] = location.Country.ISOCode
	}

	return tags
}

func (s *Server) getFieldsFromRequest(req *CheckUpgradeRequest) map[string]interface{} {
	fields := map[string]interface{}{
		utils.ToSnakeCase(ValueFieldKey): ValueFieldValue,
	}
	fields[utils.ToSnakeCase(ValueFieldKey)] = ValueFieldValue
	for k, v := range req.ExtraFieldInfo {
		if s.ValidateExtraInfo(k, v, extraInfoTypeField) {
			fields[utils.ToSnakeCase(k)] = v
		}
	}

	return fields
}
