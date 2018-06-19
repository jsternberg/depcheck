# Depcheck

Verifies that none of the locked dependencies in the current project conflict with the locked dependencies in the specified project.

## Usage

```bash
$ depcheck github.com/influxdata/influxdb
```

This would verify that the `Gopkg.lock` file from this project does not conflict with the `Gopkg.lock` file from the influxdb project.
