FROM heroiclabs/nakama-pluginbuilder:3.38.0 AS builder
WORKDIR /backend
COPY . .
RUN go build --trimpath --mod=vendor -buildmode=plugin -o ./modules/tictactoe.so .

FROM heroiclabs/nakama:3.38.0
COPY --from=builder /backend/modules/tictactoe.so /nakama/data/modules/

EXPOSE 7349 7350 7351

