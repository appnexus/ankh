THIS_MAKEFILE = $(lastword $(MAKEFILE_LIST))
REPOROOT = $(abspath $(dir $(THIS_MAKEFILE)))

export GOPATH := $(REPOROOT)/

.PHONY: all
all: ankh

.PHONY: clean
clean:
	@rm -rf $(REPOROOT)/bin
	@rm -rf $(REPOROOT)/pkg

.PHONY: ankh
ankh:
	cd $(REPOROOT)/src/ankh/cmd/ankh; go install

.PHONY: install
install: ankh
	sudo cp -f $(REPOROOT)/bin/ankh /usr/local/bin/ankh

.PHONY: cover
cover:
	cd $(REPOROOT)/src/ankh &&\
		go test -coverprofile=coverage/coverage.out &&\
		go tool cover -html=coverage/coverage.out
