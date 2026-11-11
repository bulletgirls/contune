# 이음 시연 이미지: eum + 시연용 업무 프로세스 둘(체결 matching_svc, 시세 quote_svc) + perf
# 빌드 컨텍스트는 저장소 루트: docker build -f deploy/Dockerfile .
ARG GO_VERSION=1.27.1

FROM golang:${GO_VERSION}-alpine AS eum-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/eum ./cmd/eum

# 운영 빌드와 같은 조건(-O2, strip 안 함). demo/build.sh가 그대로 쓴다.
FROM alpine:3.20 AS demo-build
RUN apk add --no-cache build-base
WORKDIR /src
COPY demo ./demo
RUN sh demo/build.sh /out

# Debian 계열의 perf는 커널 버전별 래퍼라 컨테이너에서 자주 실패한다. 사전 점검(02 §13)과 같은 alpine을 쓴다.
FROM alpine:3.20
RUN apk add --no-cache perf procps tzdata \
 && adduser -D -u 10001 eum \
 && mkdir -p /var/lib/eum/logs /var/lib/eum/work /opt/eum-demo/bin /opt/eum-demo/scripts \
 && chown -R eum:eum /var/lib/eum
COPY --from=eum-build /out/eum /usr/local/bin/eum
COPY --from=demo-build /out/matching_svc /out/quote_svc /opt/eum-demo/bin/
COPY deploy/scripts/ /opt/eum-demo/scripts/
USER eum
WORKDIR /var/lib/eum
ENTRYPOINT ["eum"]
