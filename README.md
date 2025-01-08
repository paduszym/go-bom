# `go-bom`

Simple Go library for encoding/decoding data in Apple BOM format.

## Encoding

```go
package main

import (
	"github.com/paduszym/go-bom/pkg/bom"
	"log"
	"os"
)

type Person struct {
	Name    string `bom:"Name"`
	Surname string `bom:"Surname"`
}

func main() {
	f, _ := os.Create("person.bom")
	defer f.Close()

	p := Person{Name: "John", Surname: "Doe"}
	if err := bom.NewEncoder(f).Encode(p); err != nil {
		log.Fatal(err)
	}
}

```

## Decoding

```go
package main

import (
	"fmt"
	"github.com/paduszym/go-bom/pkg/bom"
	"log"
	"os"
)

type Person struct {
	Name    string `bom:"Name"`
	Surname string `bom:"Surname"`
}

func main() {
	f, _ := os.Open("person.bom")
	defer f.Close()

	var p Person
	if err := bom.NewDecoder(f).Decode(&p); err != nil {
		log.Fatal(err)
	}

	fmt.Println(p)
}

```

## Credits

* https://github.com/hogliux/bomutils
* https://github.com/iineva/bom
