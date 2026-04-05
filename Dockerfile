FROM heroiclabs/nakama-pluginbuilder:3.38.0 AS builder
WORKDIR /backend
COPY . .
RUN go mod download
RUN go mod vendor
RUN go build --trimpath --mod=vendor -buildmode=plugin -o ./modules/tictactoe.so .

FROM heroiclabs/nakama:3.38.0
COPY --from=builder /backend/modules/tictactoe.so /nakama/data/modules/

EXPOSE 7349 7350 7351

CMD ["/bin/sh", "-ecx", \
  "/nakama/nakama migrate up --database.address postgres:arbPheIybsQyTdRNyNEzCYbysBLTYIWN@junction.proxy.rlwy.net:55876/railway && \
  /nakama/nakama \
  --database.address postgres:arbPheIybsQyTdRNyNEzCYbysBLTYIWN@junction.proxy.rlwy.net:55876/railway \
  --socket.server_key defaultkey \
  --session.token_expiry_sec 7200 \
  --console.username admin \
  --console.password admin \
  --logger.level DEBUG"]