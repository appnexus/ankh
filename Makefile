THIS_MAKEFILE = $(lastword $(MAKEFILE_LIST))
REPOROOT = $(abspath $(dir $(THIS_MAKEFILE)))
TEST_PACKAGES := ankh config context helm kubectl util

export VERSION ?= DEVELOPMENT
export GOCMD ?= go
export GOTEST=$(GOCMD) test

.PHONY: all
all: ankh

.PHONY: ankh
ankh:
	$(GOCMD) build -o ankh/ankh -ldflags "-X main.AnkhBuildVersion=$(VERSION)" ./ankh

.PHONY: clean
clean:
	@rm -f ankh/ankh && rm -rf release/

.PHONY: install
install:
	$(GOCMD) install -ldflags "-X main.AnkhBuildVersion=$(VERSION)" ./ankh

.PHONY: release
release:
	@./release.bash

.PHONY: cover-clean
cover-clean:
	@rm -f $(REPOROOT)/coverage/*

.PHONY: cover-generate
cover-generate: cover-clean
	@$(foreach p,$(TEST_PACKAGES),$(GOCMD) test github.com/appnexus/ankh/$(p) -coverprofile=coverage/$(subst /,_,$(p)).out;)
	@cat $(REPOROOT)/coverage/*.out | awk 'NR==1 || !/^mode/' > $(REPOROOT)/coverage.txt

.PHONY: cover
cover: cover-generate

.PHONY: cover-html
cover-html: cover-generate
	@$(GOCMD) tool cover -html=$(REPOROOT)/coverage.txt

.PHONY: test
test:
	$(GOTEST) -v ./...
