# assumes your GOPATH is set, which is default in 1.8 and later

dpkg-memstats: dpkg-memstats.go packagemap.go
	go build $^

go-humanize:
	go get github.com/dustin/go-humanize

clean:
	rm dpkg-memstats
