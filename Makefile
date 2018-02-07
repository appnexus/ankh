PACKAGE = ankh
THIS_MAKEFILE = $(lastword $(MAKEFILE_LIST))
REPOROOT = $(abspath $(dir $(THIS_MAKEFILE)))
BASE = "$(REPOROOT)/src/ankh"

export GOPATH := $(REPOROOT)/

PKGS = $(shell cd $(BASE) && \
       go list ./... | grep -v /vendor/)

.PHONY: all
all: ankh

.PHONY: clean
clean:
	@rm -rf $(REPOROOT)/bin
	@rm -rf $(REPOROOT)/pkg

.PHONY: ankh
ankh:
	cd $(BASE)/cmd/ankh; go install

.PHONY: install
install: ankh
	sudo cp -f $(REPOROOT)/bin/ankh /usr/local/bin/ankh

.PHONY: test
test: 
	cd $(BASE) &&\
		go test $(PKGS)

.PHONY: cover
cover:
	cd $(BASE) &&\
		go test -coverprofile=coverage/coverage.out &&\
		go tool cover -html=coverage/coverage.out
