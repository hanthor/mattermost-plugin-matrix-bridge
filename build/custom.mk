# Include custom targets and environment variables here

# Complement-compatible homeserver image used by the end-to-end suite.
COMPLEMENT_BASE_IMAGE ?= ghcr.io/element-hq/synapse/complement-synapse:develop

## Runs the Complement end-to-end suite against a real Synapse and Mattermost (needs Docker).
.PHONY: e2e
e2e: dist
	cd e2e && COMPLEMENT_BASE_IMAGE=$(COMPLEMENT_BASE_IMAGE) $(GO) test -count=1 -v -timeout 60m ./... $(E2E_TEST_FLAGS)

## Checks that the end-to-end suite compiles without running it.
.PHONY: e2e-check
e2e-check:
	cd e2e && $(GO) vet ./... && $(GO) test -count=1 -run '^$$' ./...
