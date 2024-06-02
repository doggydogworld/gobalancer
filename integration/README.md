# integration

Some testing to make sure that the load balancer works correctly. Includes some commands for easy setup and teardown

## cURL

Just using normal curl to test. Setup will just include starting a `hello-world` docker container.

Setup Commands
```
docker run -d --name web1 -p 8000:80 nginxdemos/hello:plain-text
docker run -d --name web2 -p 8001:80 nginxdemos/hello:plain-text
docker run -d --name web3 -p 8002:80 nginxdemos/hello:plain-text
```

Teardown
```
docker rm -f web1 web2 web3
```

Test command
```
curl --cert libsql/sre.crt --key libsql/sre.key --cacert libsql/root.crt https://127.0.0.1:9000
```


## libsql

Testing with a replicated DB using WSS for transport.

This test creates 8 goroutines that update a counter in the DB and one loop that reads that counter.

Setup commands

```
docker network create -d bridge libsql
docker run --name sqld --network=libsql -p 8100:8080 -d -e SQLD_NODE=primary ghcr.io/tursodatabase/libsql-server:latest
docker run --name sqld-replica1 --network=libsql -p 8101:8080 -d -e SQLD_NODE=replica -e SQLD_PRIMARY_URL=http://sqld:5001 ghcr.io/tursodatabase/libsql-server:latest
docker run --name sqld-replica2 --network=libsql -p 8102:8080 -d -e SQLD_NODE=replica -e SQLD_PRIMARY_URL=http://sqld:5001 ghcr.io/tursodatabase/libsql-server:latest
```

Teardown
```
docker rm -f sqld sqld-replica1 sqld-replica2
docker network rm libsql
```

Test command

```
go run ./libsql
```