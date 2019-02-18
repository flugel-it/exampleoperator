FROM golang:1.11.5-alpine3.9 AS builder
RUN apk update && apk upgrade 
RUN apk add --update make git bash
WORKDIR $GOPATH/src/github.com/flugel-it/exampleoperator/
COPY . .
RUN make build OUT=/exampleoperator

FROM scratch
COPY --from=builder /exampleoperator /usr/local/bin/exampleoperator
CMD ["/usr/local/bin/exampleoperator"]