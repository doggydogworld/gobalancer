package main

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

//go:embed *.key
//go:embed *.crt
var CertsFS embed.FS

func queryCount(db *sql.DB) {
	rows, err := db.Query("SELECT * FROM counter")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to execute query: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	var name string
	var count int
	for rows.Next() {
		err = rows.Scan(&name, &count)
		if err != nil {
			fmt.Printf("Error scanning row: %v\n", err)
		}
	}
	fmt.Printf("Current count: %d\n", count)
}

func incrementCount(db *sql.DB) {
	_, err := db.Exec("INSERT INTO counter(name, count) VALUES('counter', 1) ON CONFLICT(name) DO UPDATE SET count = IFNULL(count, 0) + 1;")
	if err != nil {
		fmt.Printf("Could not update counter %v\n", err)
	}
}

func init() {
	caCert, err := CertsFS.ReadFile("root.crt")
	if err != nil {
		panic(err)
	}
	userCert, err := CertsFS.ReadFile("sre.crt")
	if err != nil {
		panic(err)
	}
	userKey, err := CertsFS.ReadFile("sre.key")
	if err != nil {
		panic(err)
	}
	crt, err := tls.X509KeyPair(userCert, userKey)
	if err != nil {
		log.Fatalf("error getting user certificate")
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{
		Certificates: []tls.Certificate{crt},
		RootCAs:      caCertPool,
	}
	tr.IdleConnTimeout = time.Second / 2.0
	http.DefaultTransport = tr
}

func main() {
	url := "wss://127.0.0.1:9001"

	db, err := sql.Open("libsql", url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open db %s: %s", url, err)
		os.Exit(1)
	}
	defer db.Close()

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS counter( name TEXT PRIMARY KEY, count INT NOT NULL ) WITHOUT ROWID;")
	if err != nil {
		fmt.Printf("failed to create table %v\n", err)
	}

	for range 8 {
		go func() {
			db, err := sql.Open("libsql", url)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to open db %s: %s", url, err)
				os.Exit(1)
			}
			defer db.Close()
			for {
				incrementCount(db)
				time.Sleep(time.Second)
			}
		}()
	}
	for {
		queryCount(db)
		time.Sleep(time.Second)
	}
}
