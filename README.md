# Upgrade Responder[![Build Status](https://drone-publish.longhorn.io/api/badges/longhorn/upgrade-responder/status.svg)](https://drone-publish.longhorn.io/longhorn/upgrade-responder)

## Overview
Upgrade Responder provides a way to notify the applications running remotely when there is a new version of the application become available. 
It will also keep a record of how many requests have been sent to the Upgrade Responder server during a certain period of time(e.g. one hour), to estimate how many instances of applications are running during that period.

Upgrade Responder server doesn't store any information that can be potentially used to identify the client that makes the request, including IP address. See [here](#what-data-will-be-stored) for a full list of data that will be stored.

For example, Longhorn uses Upgrade Responder project to create the public metric dashboard at [https://metrics.longhorn.io/](https://metrics.longhorn.io/)


## How it works
1. Upgrade Responder server is set up at an application-specified endpoint.
1. A response JSON file (e.g. [upgrade-response.json](#response-json-config-example)) is provided to the server as the response to the incoming request.
   1. In Kubernetes deployment, the response JSON is normally configured as a Config Map and can be easily swapped out when a new version is available.
1. The application now can make HTTP requests in a certain format to the application-specified endpoint periodically, and get the information about the latest available version.
   1. Server will expect each instance of the application makes a request **once per hour**.
1. Once the server received a request, it will:
   1. Check where the request is coming from.
   1. Remove the IP from the request.
   1. Store the information into an InfluxDB table.

### What data will be stored
* Application Version
* Country and City of the request originated from
* (Optional) Application specified information that's can be helpful to identify the upgradability, e.g. Kubernetes version that application is running on.

## Prerequisite
1. InfluxDB <= 1.8.x is running. We currently only support InfluxDB version <= 1.8.x.
1. Grafana v7.x is running

## Usage

### 1. Running Upgrade Responder server
Start Upgrade Responder server by the command:
```
./bin/upgrade-responder --debug start <FLAGS>
```
The available flags are:

| Flag  | Example value  | Description  |
|---|---|---|
| `--upgrade-response-config` | `/etc/upgrade-responder/upgrade-response.json` | Specify the response configuration file for upgrade query. The Upgrade Responder server uses this file to determine the latest version of the application. See [upgrade-response.json](#response-json-config-example) for an example of a configuration file  |
| `--application-name` | `awesome_app` | Specify the name of the application that is using this Upgrade Responder server. This will be used to create a database named `<application-name>_upgrade_responder` in the InfluxDB to store all data for this Upgrade Responder |
| `--influxdb-url` | `http://localhost:8086` | Specify the URL of InfluxDB. Note that we currently only support InfluxDB version 1.8 and before  |
| `--influxdb-user` | `admin` | Specify the InfluxDB username |
| `--influxdb-pass` | `password` | Specify the InfluxDB password |
| `--query-period` | `1h` | Specify the period for how often each instance of the application makes the request. Cannot change after set for the first time See [here](#the-flag---query-period) for more details |
| `--geodb` | `/etc/upgrade-responder/GeoLite2-City.mmdb` | Specify the path of to GeoDB file.  See [Geography database](#geography-database) for more details about GeoDB |
| `--port` | `8314` | Specify the port number. By default port `8314` is used |

If you are deploying Upgrade Responder Server in Kubernetes, you can use our provided [chart](./chart).

> **Note:** For now, the Upgrade Responder server needs to sit behind a LoadBalancer or a proxy to get the client's IP from the field `X-Forwarded-For` and extract the location from the IP. 
> If you are deploying the Upgrade Responder server in Kubernetes, you can create an ingress to the `xxx-upgrade-responder` service so that Upgrade Responder server is behind a LoadBalancer. 
> Then send the request to the ingress's domain.

As a quick way to check Upgrade Responder server is up and running, make a request to it:
```shell
curl -X POST http://<SERVER-IP>:8314/v1/checkupgrade \
     -d '{ "appVersion": "v0.8.1", "extraInfo": {}}' 
```
If the server is running correctly, you should receive a response contains the application's latest version stored in [upgrade-response.json](#response-json-config-example):
```shell
{"versions":[{"Name":"v1.0.0","ReleaseDate":"2020-05-30T10:20:00Z","Tags":["latest"]}],"requestIntervalInMinutes":60}
```

The InfluxDB should contain a new record:
```bash
:~$ influx
> USE <application-name>_upgrade_responder
> SELECT * FROM upgrade_request
name: upgrade_request
time                app_version city     country       country_isocode  x_request_id
----                ----------- ----     -------       ---------------  ------------
1620949031026036556 v0.8.1      San Jose United States US               ebbb83a-a224-49f9-aa73-8563d1da89b7
```

### 2. Creating Grafana dashboard
Assume that you already have a running Grafana instance. Use the following steps to create a Grafana dashboard to display statical graphs:
1. Install the `Worldmap Panel` plugin to your Grafana instance by following the instruction at [here](https://grafana.com/grafana/plugins/grafana-worldmap-panel/?tab=installation)
1. Setup a DataSource in your Grafana instance to point to `<application-name>_upgrade_responder` database in the InfluxDb 
1. Import [this Grafana dashboard template](https://grafana.com/grafana/dashboards/14429).
   While in the importing form, change the DataSource to point to the DataSource in step 2 above
   and change the `AppName` to be the name of your application.
   ![Alt text](./assets/images/import_grafana_dashboard_for_upgrade_responder.png?raw=true)
1. If you specify `-query-period` to be different than `1h`, 
   you need to change the time in GROUP BY clause in Grafana dashboard queries to be multiple of `-query-period` value

Upon success, you should see a dashboard similar to:
![Alt text](./assets/images/longhorn_upgrade_responder_dashboard.png?raw=true)

### 3. Modifying your application

Modify your application to periodically send the request to Upgrade Responder server every `requestIntervalInMinutes` where `requestIntervalInMinutes` is the value returned from Upgrade Responder sever when your application makes an upgrade request to it. 
Your application should send one `POST` request every `requestIntervalInMinutes`. 
The request's body should be in the format:
   ```json
   {
    "appVersion": "v0.8.1",
    "extraInfo": {
        "fieldKey": "value"
    }
   }
   ```
By default, Upgrade Responder only groups data by `appVersion` field and creates Grafana panel for `appVersion` field. 
If you add extra fields and want to display statical information for those fields, there are extra steps you need to follow to setup InfluxDB and Grafana. 
See [Add kubernetesVersion extra field](#add-kubernetesVersion-extra-field) for an example of how to add an extra field. 

#### Go client
If your application is written in Golang, you can import our provided [client package](./client) and use it to save time writing code. 
See our [example](./example) for how to use the client package.


## References

### Response JSON config example
```
{
	"Versions": [{
		"Name": "v1.0.0",
		"ReleaseDate": "2020-05-30T10:20:00Z",
		"Tags": ["latest"]
	}]
}
```

### Add kubernetesVersion extra field
For example, if you want to keep track of the number of your application instances by each Kubernetes version, you may want to include Kubernetes version into `extraInfo` in the request's body sent to Upgrade Responder server.
The request's body may look like this:
```json
{
    "appVersion": "v0.8.1",
    "extraInfo": {
        "kubernetesVersion": "v1.19.1"
    }
}
```
In order to display statical information about Kubernetes version on Grafana dashboard, you need to do:
1. Create a continuous query in the InfluxDB to periodically group data by Kubernetes version
   ```
   CREATE CONTINUOUS QUERY "cq_by_kubernetes_version_down_sampling" ON "<application-name>_upgrade_responder" BEGIN SELECT count("value") as total INTO "by_kubernetes_version_down_sampling" FROM "upgrade_request" GROUP BY time(1h),"kubernetes_version" END
   ```
   where:
   * `cq_by_kubernetes_version_down_sampling` is the name of the new continuous query (chosen by you)
   * `<application-name>_upgrade_responder` is the name of the database prefixed by your application's name
   * `by_kubernetes_version_down_sampling` is the name of the new measurement (chosen by you)
   * `upgrade_request` is the original measurement where Upgrade Responder server records data to
   * `1h` is the query period. Make sure that this value is the same as `--query-period`
   * `kubernetes_version` is the tag to grouping data. This is derived from the camelcase form of `kubernetesVersion` to its snakecase form.

1. Create a Grafana panel that pull data from the new measurement `by_kubernetes_version_down_sampling` similar to this:
   ![Alt text](./assets/images/grafana_query_by_kubernetes_version.png?raw=true)

### The flag `--query-period`
This value should match the frequency that your application send requests to the Upgrade Responder server.
This value should also match time in GROUP BY clause in Grafana queries.

For example, if your application is sending one request to the Upgrade Responder server two hours. 
This value should be set to 2h. You also need to set 2h for the time in GROUP BY clause in Grafana queries.

By default, the `--query-period` is set to `1h`. 
Your application will need to send request to Upgrade Responder server every hour.

Normally, `--query-period` should be decided at the beginning and should not be changed after. 
Changing the value may cause temporary data mismatch in InfluxDB. If you need to change `--query-period`, please follow the steps:

1. Manually drop all continuous queries in `<application-name>_upgrade_responder`
   ```shell
   influx -username <your-username> -password <your-password>
   > SHOW CONTINUOUS QUERIES
   # all continuous queries in the database <application-name>_upgrade_responder are displayed
   > DROP CONTINUOUS QUERY <continuous-query-name> ON <application-name>_upgrade_responder
   ```
1. Restart Upgrade Responder server with new value for `--query-period`
1. Change the time in GROUP BY clause in Grafana dashboard queries to match `--query-period`
1. Modify your application to send requests to Upgrade Responder server every `--query-period` interval

See [here](https://docs.influxdata.com/influxdb/v1.8/query_language/continuous_queries/#examples-of-basic-syntax) for more details about InfluxDB continuous queries.

### Geography database

This project includes GeoLite2 data created by MaxMind, available from [here](https://www.maxmind.com).

This program doesn't store IP. Only the city level geographic data is recorded.

## For Contributor

### 1. Building Upgrade Responder project

`make`

### 2. Running locally 

`./bin/upgrade-responder --debug start --upgrade-response-config /etc/upgrade-responder/upgrade-response.json --application-name postman --influxdb-url http://localhost:8086 --influxdb-user admin --influxdb-pass admin123 --geodb /etc/upgrade-checker/GeoLite2-City.mmdb`

It will listen on `0.0.0.0:8314`


## License
Copyright (c) 2021 Longhorn Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

[http://www.apache.org/licenses/LICENSE-2.0](http://www.apache.org/licenses/LICENSE-2.0)

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
