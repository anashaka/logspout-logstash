SOURCEDIR       := .
SOURCES         := $(shell find $(SOURCEDIR) -name '*.go')
COVER_PROFILES  := $(shell go list -f '{{if gt (len .TestGoFiles) 0}}{{.Name}}.coverprofile{{end}}' ./...)
COVERALLS_TOKEN := $(shell echo $$COVERALLS_TOKEN)

.PHONY: deps clean test coveralls view-coverage

deps:
	go get -u github.com/mattn/goveralls
	go get -u github.com/wadey/gocovmerge
	go get -u github.com/jstemmer/go-junit-report
	go get -t -d -v ./...

$(COVER_PROFILES): $(SOURCES)
	go list -f '{{if gt (len .TestGoFiles) 0}}"go test -v -covermode count -coverprofile {{.Name}}.coverprofile {{.ImportPath}}"{{end}}' ./... | xargs -I {} bash -c {}

coverage.out: $(COVER_PROFILES)
	gocovmerge `ls *.coverprofile` > coverage.out

report.xml: $(SOURCES)
	go test -v ./... | tee /dev/stderr | go-junit-report > report.xml

test: report.xml

coveralls: coverage.out
	goveralls -coverprofile=coverage.out -service=circle-ci -repotoken $(COVERALLS_TOKEN)

view-coverage: coverage.out
	go tool cover -html=coverage.out

clean:
	rm -f $(COVER_PROFILES) coverage.out report.xml
