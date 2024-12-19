.PHONY: build run clean test

build:
	go build -o bin/cctvserver cmd/cctvserver/main.go
	go build -o bin/camerasim cmd/camsim/main.go

run-server:
	./bin/cctvserver

run-sim:
	./bin/camerasim -id cam1 -addr "ws://localhost:8080/camera/connect"

clean:
	rm -rf bin/
	rm -rf frames/

test:
	go test ./...

video:
	ffmpeg -framerate 30 -pattern_type glob -i 'frames/cam1/*.jpg' \
		-c:v libx264 -pix_fmt yuv420p -t 120 output.mp4
