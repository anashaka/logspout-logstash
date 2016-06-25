SOURCEDIR       := .
SOURCES         := $(shell find $(SOURCEDIR) -name '*.go')
REPORTS         := $(shell go list -f '{{if gt (len .TestGoFiles) 0}}{{.Name}}.coverprofile{{end}}' ./...) report.xml coverage.out
COVERALLS_TOKEN := $(shell echo $$COVERALLS_TOKEN)

.PHONY: deps clean test coveralls view-coverage

deps:
	go get -u github.com/mattn/goveralls
	go get -u github.com/wadey/gocovmerge
	go get -u github.com/jstemmer/go-junit-report
	go get -t -d -v ./...

$(REPORTS): $(SOURCES)
	go list -f '{{if gt (len .TestGoFiles) 0}}"go test -v -covermode count -coverprofile {{.Name}}.coverprofile {{.ImportPath}}"{{end}} | tee -a tmpreport.out' ./... | xargs -I {} bash -c {}
	cat tmpreport.out | go-junit-report > report.xml
	gocovmerge `ls *.coverprofile` > coverage.out
	rm tmpreport.out

test: $(REPORTS)

coveralls: coverage.out
	goveralls -coverprofile=coverage.out -service=circle-ci -repotoken $(COVERALLS_TOKEN)

view-coverage: coverage.out
	go tool cover -html=coverage.out

clean:
	rm -f $(REPORTS)
