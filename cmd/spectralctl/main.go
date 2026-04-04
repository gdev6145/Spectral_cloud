package main

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
	"github.com/gdev6145/Spectral_cloud/pkg/store"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schemas/*.json
var schemaFS embed.FS

type blockchainFile struct {
	Version   int                `json:"version"`
	UpdatedAt string             `json:"updated_at"`
	Blocks    []blockchain.Block `json:"blocks"`
}

type routesFile struct {
	Version   int             `json:"version"`
	UpdatedAt string          `json:"updated_at"`
	Routes    []routing.Route `json:"routes"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "validate":
		validateCmd(os.Args[2:])
	case "repair":
		repairCmd(os.Args[2:])
	case "backup":
		backupCmd(os.Args[2:])
	case "compact":
		compactCmd(os.Args[2:])
	case "keygen":
		keygenCmd(os.Args[2:])
	case "rekey":
		rekeyCmd(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  spectralctl validate --data-dir <dir>")
	fmt.Fprintln(os.Stderr, "  spectralctl repair --data-dir <dir>")
	fmt.Fprintln(os.Stderr, "  spectralctl backup --db-path <path> --out <path> [--key <base64>]")
	fmt.Fprintln(os.Stderr, "  spectralctl compact --db-path <path> --out <path>")
	fmt.Fprintln(os.Stderr, "  spectralctl compact --db-path <path> --in-place")
	fmt.Fprintln(os.Stderr, "  spectralctl keygen")
	fmt.Fprintln(os.Stderr, "  spectralctl rekey --in <path> --out <path> --old-key <base64> --new-key <base64>")
}

func validateCmd(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	dataDir := fs.String("data-dir", "data", "data directory")
	dbPath := fs.String("db-path", "", "db path (optional)")
	_ = fs.Parse(args)

	chainPath := *dataDir + "/blockchain.json"
	routesPath := *dataDir + "/routes.json"
	if *dbPath == "" {
		*dbPath = store.DBPath(*dataDir)
	}

	chainIssues, err := validateBlockchain(chainPath, *dbPath)
	if err != nil {
		exitErr(err)
	}
	routesIssues, err := validateRoutes(routesPath, *dbPath)
	if err != nil {
		exitErr(err)
	}
	if err := validateSchemaFile(chainPath, "schemas/blockchain.schema.json"); err != nil {
		chainIssues = append(chainIssues, "schema: "+err.Error())
	}
	if err := validateSchemaFile(routesPath, "schemas/routes.schema.json"); err != nil {
		routesIssues = append(routesIssues, "schema: "+err.Error())
	}
	printIssues("blockchain", chainIssues)
	printIssues("routes", routesIssues)
}

func repairCmd(args []string) {
	fs := flag.NewFlagSet("repair", flag.ExitOnError)
	dataDir := fs.String("data-dir", "data", "data directory")
	dbPath := fs.String("db-path", "", "db path (optional)")
	_ = fs.Parse(args)

	chainPath := *dataDir + "/blockchain.json"
	routesPath := *dataDir + "/routes.json"
	if *dbPath == "" {
		*dbPath = store.DBPath(*dataDir)
	}

	if err := repairBlockchain(chainPath, *dbPath); err != nil {
		exitErr(err)
	}
	if err := repairRoutes(routesPath, *dbPath); err != nil {
		exitErr(err)
	}
	fmt.Println("repair completed")
}

func backupCmd(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dbPath := fs.String("db-path", "", "db path")
	outPath := fs.String("out", "", "output path")
	key := fs.String("key", "", "base64-encoded 32-byte key (optional)")
	_ = fs.Parse(args)
	if *dbPath == "" || *outPath == "" {
		exitErr(errors.New("db-path and out are required"))
	}
	if strings.TrimSpace(*key) == "" {
		if err := store.Backup(*dbPath, *outPath); err != nil {
			exitErr(err)
		}
		if err := store.Verify(*outPath); err != nil {
			exitErr(err)
		}
		fmt.Println("backup completed")
		return
	}
	if err := store.BackupEncrypted(*dbPath, *outPath, *key); err != nil {
		exitErr(err)
	}
	if err := store.VerifyEncrypted(*outPath, *key); err != nil {
		exitErr(err)
	}
	fmt.Println("backup completed (encrypted)")
}

func compactCmd(args []string) {
	fs := flag.NewFlagSet("compact", flag.ExitOnError)
	dbPath := fs.String("db-path", "", "db path")
	outPath := fs.String("out", "", "output path")
	inPlace := fs.Bool("in-place", false, "compact in place")
	_ = fs.Parse(args)
	if *dbPath == "" {
		exitErr(errors.New("db-path is required"))
	}
	if !*inPlace && *outPath == "" {
		exitErr(errors.New("out is required unless --in-place is set"))
	}
	if *inPlace {
		if err := store.CompactInPlace(*dbPath); err != nil {
			exitErr(err)
		}
		fmt.Println("compact completed (in-place)")
		return
	}
	if err := store.Compact(*dbPath, *outPath); err != nil {
		exitErr(err)
	}
	if err := store.Verify(*outPath); err != nil {
		exitErr(err)
	}
	fmt.Println("compact completed")
}

func keygenCmd(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	_ = fs.Parse(args)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		exitErr(err)
	}
	fmt.Println(base64.StdEncoding.EncodeToString(key))
}

func rekeyCmd(args []string) {
	fs := flag.NewFlagSet("rekey", flag.ExitOnError)
	inPath := fs.String("in", "", "input encrypted backup")
	outPath := fs.String("out", "", "output encrypted backup")
	oldKey := fs.String("old-key", "", "old base64 key")
	newKey := fs.String("new-key", "", "new base64 key")
	_ = fs.Parse(args)
	if *inPath == "" || *outPath == "" || *oldKey == "" || *newKey == "" {
		exitErr(errors.New("in, out, old-key, and new-key are required"))
	}
	if err := store.RotateEncryptedBackup(*inPath, *outPath, *oldKey, *newKey); err != nil {
		exitErr(err)
	}
	fmt.Println("rekey completed")
}

func printIssues(name string, issues []string) {
	if len(issues) == 0 {
		fmt.Printf("%s: ok\n", name)
		return
	}
	fmt.Printf("%s: %d issue(s)\n", name, len(issues))
	for _, issue := range issues {
		fmt.Printf("- %s\n", issue)
	}
}

func validateBlockchain(path, dbPath string) ([]string, error) {
	if blocks, ok, err := readBlocksFromDB(dbPath); err != nil {
		return nil, err
	} else if ok {
		issues := validateBlocks(blocks)
		if err := validateSchemaDoc(blocks, "schemas/blockchain.schema.json"); err != nil {
			issues = append(issues, "schema: "+err.Error())
		}
		return issues, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var file blockchainFile
	if err := json.Unmarshal(data, &file); err == nil && len(file.Blocks) > 0 {
		return validateBlocks(file.Blocks), nil
	}
	var blocks []blockchain.Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil, err
	}
	return validateBlocks(blocks), nil
}

func validateBlocks(blocks []blockchain.Block) []string {
	var issues []string
	var prevHash string
	for i, block := range blocks {
		if !blockchain.Verify(&block) {
			issues = append(issues, fmt.Sprintf("block %d has invalid hash", i))
			break
		}
		if block.Index > 0 && block.PreviousHash != prevHash {
			issues = append(issues, fmt.Sprintf("block %d previous hash mismatch", i))
			break
		}
		prevHash = block.Hash
	}
	return issues
}

func validateRoutes(path, dbPath string) ([]string, error) {
	if routes, ok, err := readRoutesFromDB(dbPath); err != nil {
		return nil, err
	} else if ok {
		issues := validateRouteList(routes)
		if err := validateSchemaDoc(routes, "schemas/routes.schema.json"); err != nil {
			issues = append(issues, "schema: "+err.Error())
		}
		return issues, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var file routesFile
	if err := json.Unmarshal(data, &file); err == nil && len(file.Routes) > 0 {
		return validateRouteList(file.Routes), nil
	}
	var routes []routing.Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return nil, err
	}
	return validateRouteList(routes), nil
}

func validateRouteList(routes []routing.Route) []string {
	var issues []string
	now := time.Now().UTC()
	for _, route := range routes {
		if route.Destination == "" {
			issues = append(issues, "route with empty destination")
			break
		}
		if route.ExpiresAt != nil && now.After(*route.ExpiresAt) {
			issues = append(issues, fmt.Sprintf("route %s expired", route.Destination))
		}
	}
	return issues
}

func validateSchemaFile(path, schemaPath string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	return validateSchemaDoc(doc, schemaPath)
}

func validateSchemaDoc(doc any, schemaPath string) error {
	schemaBytes, err := schemaFS.ReadFile(schemaPath)
	if err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	if err := compiler.AddResource(schemaPath, strings.NewReader(string(schemaBytes))); err != nil {
		return err
	}
	schema, err := compiler.Compile(schemaPath)
	if err != nil {
		return err
	}
	if err := schema.Validate(doc); err != nil {
		return err
	}
	return nil
}

func repairBlockchain(path, dbPath string) error {
	if blocks, ok, err := readBlocksFromDB(dbPath); err != nil {
		return err
	} else if ok {
		valid := filterValidBlocks(blocks)
		return writeBlocksToDB(dbPath, valid)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file blockchainFile
	if err := json.Unmarshal(data, &file); err == nil && len(file.Blocks) > 0 {
		valid := filterValidBlocks(file.Blocks)
		file.Blocks = valid
		file.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return writeJSON(path, file)
	}
	var blocks []blockchain.Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	valid := filterValidBlocks(blocks)
	return writeJSON(path, blockchainFile{
		Version:   1,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Blocks:    valid,
	})
}

func repairRoutes(path, dbPath string) error {
	if routes, ok, err := readRoutesFromDB(dbPath); err != nil {
		return err
	} else if ok {
		return writeRoutesToDB(dbPath, pruneExpired(routes))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file routesFile
	if err := json.Unmarshal(data, &file); err == nil && len(file.Routes) > 0 {
		file.Routes = pruneExpired(file.Routes)
		file.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return writeJSON(path, file)
	}
	var routes []routing.Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return err
	}
	return writeJSON(path, routesFile{
		Version:   1,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Routes:    pruneExpired(routes),
	})
}

func pruneExpired(routes []routing.Route) []routing.Route {
	now := time.Now().UTC()
	out := routes[:0]
	for _, route := range routes {
		if route.ExpiresAt != nil && now.After(*route.ExpiresAt) {
			continue
		}
		out = append(out, route)
	}
	return out
}

func filterValidBlocks(blocks []blockchain.Block) []blockchain.Block {
	out := make([]blockchain.Block, 0, len(blocks))
	var prevHash string
	for _, block := range blocks {
		if !blockchain.Verify(&block) {
			break
		}
		if block.Index > 0 && block.PreviousHash != prevHash {
			break
		}
		prevHash = block.Hash
		out = append(out, block)
	}
	return out
}

func readBlocksFromDB(path string) ([]blockchain.Block, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	db, err := store.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = db.Close() }()
	blocks, err := db.ReadBlocks()
	if err != nil {
		return nil, false, err
	}
	if len(blocks) == 0 {
		return nil, false, nil
	}
	return blocks, true, nil
}

func readRoutesFromDB(path string) ([]routing.Route, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	db, err := store.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = db.Close() }()
	routes, err := db.ReadRoutes()
	if err != nil {
		return nil, false, err
	}
	if len(routes) == 0 {
		return nil, false, nil
	}
	return routes, true, nil
}

func writeBlocksToDB(path string, blocks []blockchain.Block) error {
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.WriteBlocks(blocks)
}

func writeRoutesToDB(path string, routes []routing.Route) error {
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.WriteRoutes(routes)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
