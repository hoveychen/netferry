# Build gomobile libraries for iOS and Android.
#
# Prerequisites:
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   gomobile init
#
# For iOS: Xcode command-line tools
# For Android: Android NDK (set ANDROID_NDK_HOME)

MODULE  := github.com/hoveychen/netferry/relay/mobile
VERSION := $(shell date +'%y.%-m.%-d-%s')
LDFLAGS := -X $(MODULE).version=$(VERSION)

.PHONY: ios android all clean servers

all: servers ios android

# Build server binaries that get embedded into the mobile library.
servers:
	$(MAKE) -C ../cmd/tunnel servers
	cp ../cmd/tunnel/binaries/server-linux-amd64 binaries/
	cp ../cmd/tunnel/binaries/server-linux-arm64 binaries/

# Build iOS xcframework.
ios: servers
	cd .. && gomobile bind \
		-target=ios \
		-ldflags='$(LDFLAGS)' \
		-o mobile/NetFerryEngine.xcframework \
		./mobile
	@echo "Built: NetFerryEngine.xcframework"

# Build Android AAR.
android: servers
	cd .. && gomobile bind \
		-target=android \
		-androidapi=26 \
		-ldflags='$(LDFLAGS)' \
		-o mobile/mobile.aar \
		./mobile
	@echo "Built: mobile.aar"

clean:
	rm -rf NetFerryEngine.xcframework mobile.aar mobile-sources.jar
