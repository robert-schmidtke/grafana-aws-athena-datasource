# grafana aws athena datasource

Grafana plugin for queryng AWS Athena as data source

## Installation

```sh
docker run -d --name=grafana -p 3000:3000 -e GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=mtanda-aws-athena-datasource grafana/grafana

docker exec -it grafana /bin/bash
# inside container
grafana-cli --pluginUrl https://github.com/robert-schmidtke/grafana-aws-athena-datasource/releases/download/2.2.17/grafana-aws-athena-datasource-2.2.17.zip plugins install grafana-aws-athena-datasource
exit

# outside container
docker restart grafana
```

## Notable changes in this fork
- Adds default workgroup to datasource configuration.
- Adds support for dashboard variables, using default region and default workgroup from the datasource configuration.
- Tolerates "Query has not yet finished"
- Allows configuring query timeout
- Waits for query execution IDs if they're cached (prevents warnings/no data when running+caching same query multiple times concurrently)
- Removes failed query executions from cache so they can be retried
- Parallelizes execution of multiple targets
- Adds a lot more debug logging.
- Builds on Go 1.17
- Targets Grafana 7.5.10

## Building
Also checkout `.github/workflows/go.yml`.

```
# Download and install golang: https://golang.org/doc/install
sudo apt install nodejs
sudo npm install --global yarn
yarn install
make
```
