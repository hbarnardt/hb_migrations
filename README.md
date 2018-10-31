# hb migrations - A Better migration engine for [go-pg/pg](https://github.com/go-pg/pg)

## Basic Commands
- init
  - runs the specified intial migration as a batch on it's own.
- migrate
  - runs all available migrations that have not been run inside a batch
- rollback 
  - reverts the last batch of migrations.
- create <name>
  - creates a migration file using the name provided.

## Usage
Make a `main.go` in a `migrations` folder
```golang
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/go-pg/pg"
	migrations "github.com/hbarnardt/hb_migrations"
)

const usageText = "Lorem Ipsum"

func main() {
	flag.Usage = usage
	flag.Parse()

  dbCnf := ...

	db := pg.Connect(&pg.Options{
		Addr:     dbCnf.GetHost(),
		User:     dbCnf.GetUsername(),
		Database: dbCnf.GetDatabase(),
		Password: dbCnf.GetPassword(),
	})

	migrations.SetMigrationTableName("public.migrations_home")
	migrations.SetInitialMigration("000000000000_init")
	migrations.SetMigrationNameConvention(migrations.SnakeCase)

	err := migrations.Run(db, flag.Args()...)

	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Printf(usageText)
	flag.PrintDefaults()
	os.Exit(2)
}
```
