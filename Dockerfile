# Build a static probe-sim binary, then ship it on an Alpine base.
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod .
COPY main.go ui.go ./

# CGO disabled so the binary is fully static and runs anywhere.
RUN CGO_ENABLED=0 go build -o /probe-sim .

FROM alpine:3.20
COPY --from=build /probe-sim /probe-sim
EXPOSE 8080
ENTRYPOINT ["/probe-sim"]