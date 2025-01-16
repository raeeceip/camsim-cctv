.PHONY: build run clean test

build:
	go build -o bin/cctvserver cmd/cctvserver/main.go
	go build -o bin/camerasim cmd/camsim/main.go

run-server:
	mkdir -p frames/
	mkdir -p frames/videos/
	./bin/cctvserver

run-sim:
	./bin/camerasim -id cam1 -addr "ws://localhost:8080/camera/connect"

run-both:
	mkdir -p frames/
	mkdir -p frames/videos/
	./bin/cctvserver &
	./bin/camerasim -id cam1 -addr "ws://localhost:8080/camera/connect"

clean:
	rm -rf bin/
	rm -rf frames/
	rm -f output.mp4

test:
	go test ./...

video:
	@echo "Converting frames to video..."
	@find frames -type f -name "frame_*.jpg" | sort > frame_list.txt
	@if [ -s frame_list.txt ]; then \
		ffmpeg -framerate 30 -f concat -safe 0 -i frame_list.txt \
			-c:v libx264 -pix_fmt yuv420p -y output.mp4 && \
		echo "Video created successfully: output.mp4"; \
	else \
		echo "No frames found to process"; \
	fi
	@rm -f frame_list.txt