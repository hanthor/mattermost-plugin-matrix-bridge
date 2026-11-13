# Include custom targets and environment variables here

# Tests that fail on Tuwunel for reasons that are not bridge bugs (docs/HOMESERVERS.md).
TUWUNEL_SKIP ?= ^(TestArchivingAChannelTellsTheRoom|TestBackfillStructure)$$

# Complement-compatible homeserver image used by the end-to-end suite.
COMPLEMENT_BASE_IMAGE ?= ghcr.io/element-hq/synapse/complement-synapse:develop

## Runs the Complement end-to-end suite against a real Synapse and Mattermost (needs Docker).
.PHONY: e2e
e2e: dist
	cd e2e && COMPLEMENT_BASE_IMAGE=$(COMPLEMENT_BASE_IMAGE) $(GO) test -count=1 -v -timeout 60m ./... $(E2E_TEST_FLAGS)

## Builds a Complement image of Tuwunel (see docs/HOMESERVERS.md).
.PHONY: e2e-tuwunel-image
e2e-tuwunel-image:
	docker build -t local/complement-tuwunel e2e/homeservers/tuwunel

## Runs the end-to-end suite against Tuwunel instead of Synapse. Tests that
## need Synapse-only behaviour are listed in docs/HOMESERVERS.md and skipped.
.PHONY: e2e-tuwunel
e2e-tuwunel: dist e2e-tuwunel-image
	cd e2e && COMPLEMENT_BASE_IMAGE=local/complement-tuwunel $(GO) test -count=1 -v -timeout 60m ./... -skip '$(TUWUNEL_SKIP)'

## Checks that the end-to-end suite compiles without running it.
.PHONY: e2e-check
e2e-check:
	cd e2e && $(GO) vet ./... && $(GO) test -count=1 -run '^$$' ./...

## Regenerates docs/CAPABILITIES.md from e2e/capabilities.
.PHONY: capabilities
capabilities:
	cd e2e && $(GO) test ./capabilities -run TestCapabilitiesDoc -update

## Regenerates the docs/walkthrough screenshots by driving Mattermost and Element in a browser (needs Docker, Node.js and Playwright's Chromium).
.PHONY: walkthrough
walkthrough: dist
	cd e2e/walkthrough && npm install --no-audit --no-fund
	cd e2e && E2E_WALKTHROUGH=1 COMPLEMENT_BASE_IMAGE=$(COMPLEMENT_BASE_IMAGE) $(GO) test -count=1 -v -timeout 60m -run '^TestWalkthrough(Mirror)?$$' .

## Browser-level tests of what the Mattermost web client shows while Matrix users act (needs Docker, Node.js and Playwright's Chromium).
.PHONY: ui-tests
ui-tests: dist
	cd e2e/walkthrough && npm install --no-audit --no-fund
	cd e2e && E2E_UI=1 COMPLEMENT_BASE_IMAGE=$(COMPLEMENT_BASE_IMAGE) $(GO) test -count=1 -v -timeout 40m -run '^TestMattermostUI$$' .
