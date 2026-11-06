# s3baby

A tiny S3 stand-in for local development. Objects are stored as plain files under `data/<bucket>/<key>`.

It supports path-style `PUT`, `GET`, `HEAD` and `DELETE`, including presigned URLs (signatures are not checked), the AWS SDK's `aws-chunked` uploads, range requests, and CORS for uploads straight from the browser. Single binary, standard library only.

## Run

    go run .

or install it:

    go install github.com/frozturk/s3baby@latest
    s3baby

or with Docker:

    docker build -t s3baby .
    docker run -p 59000:59000 -v "$PWD/data:/data" s3baby

| Variable | Default |
| --- | --- |
| `S3BABY_HOST` | `127.0.0.1` |
| `S3BABY_PORT` | `59000` |
| `S3BABY_DATA` | `./data` |

## Use with an S3 client

Point the client at `http://127.0.0.1:59000`, use path-style addressing and any access key and secret. Buckets are created on the first upload.

Not for production: there is no authentication.

## License

MIT
