FROM golang:1.26.1-bookworm AS build
WORKDIR /src
COPY go.mod *.go ./
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-mod=mod GOTOOLCHAIN=local
RUN go build -trimpath -ldflags="-s -w" -o /out/kestrel .

FROM scratch
COPY --from=build /out/kestrel /kestrel
ENV KTUNE=armFirst=200,armYield=70,dodge=1,cubes=30,kiteLo=81,kiteLHi=193,leadFade=0.52,sightW=4,weakW=1.5
ENV KNAME=kestrel-B11-sightfocus
USER 65534:65534
ENTRYPOINT ["/kestrel"]
