# hb migrations - A Better migration engine for [go-pg/pg](https://github.com/go-pg/pg)

## Basic Commands
- init
  - runs the specified intial migration as a batch on it's own.
- migrate
  - runs all available migrations that have not been run inside a batch
- rollback 
  - reverts the last batch of migrations.
- create **name**
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

Compile it:
```bash
$> go build -i -o ./migrations/migrations ./migrations/*.go
```

Run it:
```bash
$> ./migrations/migrations migrate
```

## Notes on generated file names
```bash
$> ./migrations/migrations create new_index
```
Creates a file in the `./migrations` folder called `20181031230738_new_index.go` with the following contents:

```golang
package main

import (
	"github.com/go-pg/pg"
	migrations "github.com/hbarnardt/hb_migrations"
)

func init() {
	migrations.Register(
		"20181031230738_new_index",
		up20181031230738NewIndex,
		down20181031230738NewIndex,
	)
}

func up20181031230738NewIndex(tx *pg.Tx) error {
	_, err := tx.Exec(``)
	return err
}

func down20181031230738NewIndex(tx *pg.Tx) error {
	_, err := tx.Exec(``)
	return err
}
```

Forward migration sql commands go in up and Rollback migrations sql commands go in down

