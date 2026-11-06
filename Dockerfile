FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /s3baby .

FROM scratch
COPY --from=build /s3baby /s3baby
ENV S3BABY_HOST=0.0.0.0 S3BABY_DATA=/data
EXPOSE 59000
VOLUME /data
ENTRYPOINT ["/s3baby"]
