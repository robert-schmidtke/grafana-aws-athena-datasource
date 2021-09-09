# grafana-aws-athena-datasource
- Adds default workgroup to datasource configuration.
- Adds support for dashboard variables, using default region and default workgroup from the datasource configuration.
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
