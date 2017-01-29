# dpkg-memstats

This tool shows memory usage per Debian package. It is not part of
dpkg.

To build, simply run `make` if you have that installed, otherwise `go
build *.go` should get you started. This requires the
[dustin/go-humanize](https://github.com/dustin/go-humanize) library.

Make sure your `GOPATH` environment is correctly set (to `~/go` in
Golang 1.8 or later).
