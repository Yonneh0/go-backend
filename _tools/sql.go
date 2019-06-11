//////////////////////////////////////////////////////////////////////////////////
// sql.go - sql interface
//////////////////////////////////////////////////////////////////////////////////
package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var database *sql.DB

// connect to SQL
func sqlInit() {
	var err error

	log(nil, "connect to SQL, user:"+c.Mariadb.User)
	database, err = sql.Open("mysql", c.Mariadb.User+":"+c.Mariadb.Pass+"@tcp(127.0.0.1:3306)/?maxAllowedPacket=0")
	if err != nil {
		panic("Unable to connect to SQL!")
	}
	log(nil, "SQL init complete")

}

// attempts the query 10 times, and panics if it fails
func safeExec(query string) int64 {
	var tries int
Again:
	statement, err := database.Prepare(query)
	defer statement.Close()
	if err != nil {
		var logquery string
		if len(query) > 505 {
			logquery = query[:250] + " ... " + query[len(query)-250:]
		} else {
			logquery = query
		}
		log(nil, err)
		log(nil, fmt.Sprintf("Query was: (%d)%s", len(query), logquery))
		tries++
		if tries < 11 {
			time.Sleep(1 * time.Second)
			goto Again
		}
		panic(query)
	} else {
		res, err := statement.Exec()
		if err != nil {
			var logquery string
			if len(query) > 505 {
				logquery = query[:250] + " ... " + query[len(query)-250:]
			} else {
				logquery = query
			}
			log(nil, err)
			log(nil, fmt.Sprintf("Query was: (%d)%s", len(query), logquery))
			tries++
			if tries < 11 {
				time.Sleep(1 * time.Second)
				goto Again
			}
			panic(query)
		} else {
			aff, err := res.RowsAffected()
			if err != nil {
				log(nil, err)
				aff = 0
			}
			return aff
		}
	}
}

func safeQuery(query string) *sql.Rows {
	var tries int
Again:
	statement, err := database.Prepare(query)
	defer statement.Close()
	if err != nil {
		var logquery string
		if len(query) > 505 {
			logquery = query[:250] + " ... " + query[len(query)-250:]
		} else {
			logquery = query
		}
		log(nil, err)
		log(nil, fmt.Sprintf("Query was: (%d)%s", len(query), logquery))
		tries++
		if tries < 11 {
			time.Sleep(1 * time.Second)
			goto Again
		}
		panic(query)
	} else {
		res, err := statement.Query()
		if err != nil {
			var logquery string
			if len(query) > 505 {
				logquery = query[:250] + " ... " + query[len(query)-250:]
			} else {
				logquery = query
			}
			log(nil, err)
			log(nil, fmt.Sprintf("Query was: (%d)%s", len(query), logquery))
			tries++
			if tries < 11 {
				time.Sleep(1 * time.Second)
				goto Again
			}
			panic(query)
		} else {
			return res
		}
	}
}