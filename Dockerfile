FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/promptify ./cmd/server \
    && mkdir -p /out/data/uploads

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/promptify /promptify
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV ADDR=:8080 UPLOAD_DIR=/data/uploads
EXPOSE 8080
VOLUME /data/uploads
ENTRYPOINT ["/promptify"]
