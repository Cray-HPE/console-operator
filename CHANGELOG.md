# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.12.0] - 2024-12-19
### Fixed
- CASMTRIAGE-7594 - clean up resilience, rebalance nodes, and accept other worker nodes
- CASMCMS-9035 - remove sshd from the image since it is not needed and adds security risks.

## [1.11.0] - 2024-10-15
### Added
- CASMCMS-8681 - add add inotify-tools to the base image.

## [1.10.0] - 2024-10-03
### Changed
- CASMCMS-9147 - stop using alpine:latest base image.

## [1.9.2] - 2024-09-16
### Dependencies
- Bump `google.golang.org/protobuf` from 1.30.0 to 1.33.0 ([#66](https://github.com/Cray-HPE/console-operator/pull/66))

## [1.9.1] - 2024-09-16
### Dependencies
- Pin `google.golang.org/grpc` to resolve CVE

## [1.9.0] - 2024-09-16
### Changed
- Record Go version during build, so it is in the build log.

### Fixed
- Update Go version in `go.mod` to match the Go version we're using.

### Dependencies
- Bump `golang.org/x/crypto` from 0.0.0-20210616213533-5ff15b29337e to 0.17.0 ([#61](https://github.com/Cray-HPE/console-operator/pull/61))
- Move to Go 1.18 to resolve CVE ([#61](https://github.com/Cray-HPE/console-operator/pull/61))
- Bump `github.com/gogo/protobuf` from 1.2.2-0.20190723190241-65acae22fc9d to 1.3.2 ([#62](https://github.com/Cray-HPE/console-operator/pull/62))
- Bump `golang.org/x/net` from 0.10.0 to 0.23.0 ([#63](https://github.com/Cray-HPE/console-operator/pull/63))
- Bump `k8s/apimachinery` to 0.16.13 to resolve CVE

## [1.8.1] - 2024-09-05
### Dependencies
- CASMCMS-9136: Bump minimum `cray-services` base chart version to 11.0

## [1.8.0] - 2024-05-03
### Added
- CASMCMS-8899 - add support for Paradise (xd224) nodes.

### Changed
- Disabled concurrent Jenkins builds on same branch/commit
- Added build timeout to avoid hung builds

### Removed
- Removed defunct files leftover from previous versioning system

## [1.7.0] - 2023-04-05
### Changed
- CASMCMS-8415 - Mountain key updates are now asynchronous
- CASMCMS-8416 - Database updates include updating the node type
- CASMCMS-8252 - Enabled building of unstable artifacts
- CASMCMS-8252 - Updated header of update_versions.conf to reflect new tool options
- Added dependency injection to allow for unit testing
- Added <https://pkg.go.dev/github.com/go-chi/chi/v5@v5.0.7> for routing. Lock at v5.0.7 due to golang version bump in v5.0.8
- CASMCMS-7167 - Adding pod location API, replica API to enable monitoring resiliency.

### Fixed
 - Spelling corrections.
 - CASMCMS-8252: Update Chart with correct image and chart version strings during builds.

## [1.6.3] - 2023-02-24
### Changed
- CASMCMS-8423 - linting changes for new gofmt version.

## [1.6.2] - 2023-02-03
### Changed
- CASMTRIAGE-4899 - fix post-install and post-update hooks.

## [1.6.1] - 2022-12-20
### Added
- Add Artifactory authentication to Jenkinsfile

## [1.6.0] - 2022-08-04
### Changed
 - CASMCMS-8140: Fix handling Hill nodes.

## [1.5.0] - 2022-07-13
### Changed
 - CASMCMS-8016: Update hsm api to v2.

## [1.4.0] - 2022-07-12
### Changed
 - CASMCMS-7830: Update the base image to newer version.
