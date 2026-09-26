FROM golang:1.26 AS build
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY Makefile ./
COPY pkg/ pkg/

RUN make build

FROM gcr.io/distroless/static:nonroot
COPY --from=build workspace/bin/ns-operator /ns-operator
USER 65532:65532
ENTRYPOINT ["/ns-operator"]
