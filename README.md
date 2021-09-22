Longhorn Upgrade Responder[![Build Status](https://drone-publish.longhorn.io/api/badges/longhorn/upgrade-responder/status.svg)](https://drone-publish.longhorn.io/longhorn/upgrade-responder)
========

> **Note:** This branch (v0.0.x) is the old version that is used exclusively by the Longhorn project. 
> Longhorn eventually will need to migrate to v0.1.x which is a generic version. 
> However, the migration is a big effort because we need to migrate the InfluxDB database. 
> So, Longhorn keeps using the v0.0.x for now.

## Building

`make`


## Running

`./bin/upgrade-responder --debug start --upgrade-response-config /etc/upgrade-responder/upgrade-response.json --influxdb-url http://localhost:8086`

It will listen on `0.0.0.0:8314`

## Config file example
```
{
	"Versions": [{
		"Name": "v1.0.0",
		"ReleaseDate": "2020-05-30T10:20:00Z",
		"Tags": ["latest"]
	}]
}
```

## Geography database

This project includes GeoLite2 data created by MaxMind, available from [here](https://www.maxmind.com).

This program doesn't store IP. Only the city level geographic data is recorded.

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
