THIS_MAKEFILE = $(lastword $(MAKEFILE_LIST))
REPOROOT = $(abspath $(dir $(THIS_MAKEFILE)))
TEST_PACKAGES = $(subst $(REPOROOT)/src/,,$(shell go list -f '{{if gt (len .TestGoFiles) 0}}{{.Dir}}{{end}}' ./...))

export GOPATH := $(REPOROOT)/
export VERSION ?= DEVELOPMENT
export GOCMD ?= go

.PHONY: all
all: ankh

.PHONY: clean
clean:
	@rm -rf $(REPOROOT)/bin
	@rm -rf $(REPOROOT)/pkg
	@rm -rf $(REPOROOT)/release

.PHONY: ankh
ankh:
	cd $(REPOROOT)/src/ankh/cmd/ankh; $(GOCMD) install -ldflags "-X main.AnkhBuildVersion=$(VERSION)"

.PHONY: install
install: ankh
	sudo cp -f $(REPOROOT)/bin/ankh /usr/local/bin/ankh

.PHONY: release
release:
	@./release.bash

.PHONY: cover-clean
cover-clean:
	@rm -f $(REPOROOT)/src/ankh/coverage/*

.PHONY: cover-generate
cover-generate: cover-clean
	@cd $(REPOROOT)/src/ankh; $(foreach p,$(TEST_PACKAGES),$(GOCMD) test $(p) -coverprofile=coverage/$(subst /,_,$(p)).out;)
	@cat $(REPOROOT)/src/ankh/coverage/*.out | awk 'NR==1 || !/^mode/' > $(REPOROOT)/coverage.txt

.PHONY: cover
cover: cover-generate

.PHONY: cover-html
cover-html: cover-generate
	@$(GOCMD) tool cover -html=$(REPOROOT)/coverage.txt
