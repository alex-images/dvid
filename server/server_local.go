// +build !clustered,!gcloud

/*
	This file supports opening and managing HTTP/RPC servers locally from one process
	instead of using always available services like in a cluster or Google cloud.  It
	also manages local or embedded storage engines.
*/

package server

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/janelia-flyem/dvid/datastore"
	"github.com/janelia-flyem/dvid/dvid"
	"github.com/janelia-flyem/dvid/storage"
	"github.com/janelia-flyem/dvid/storage/local"

	"github.com/janelia-flyem/go/nrsc"
)

const (
	// The default RPC address of the DVID RPC server
	DefaultRPCAddress = "localhost:8001"

	// The name of the server error log, stored in the datastore directory.
	ErrorLogFilename = "dvid-errors.log"

	// Maximum number of throttled ops we can handle through API
	MaxThrottledOps = 1
)

var (
	// runningService is a global variable that holds the currently running
	// datastore service.
	runningService = Service{
		WebAddress: DefaultWebAddress,
		RPCAddress: DefaultRPCAddress,
	}

	// ActiveHandlers is maximum number of active handlers over last second.
	ActiveHandlers int

	// Running tally of active handlers up to the last second
	curActiveHandlers int

	// MaxChunkHandlers sets the maximum number of chunk handlers (goroutines) that
	// can be multiplexed onto available cores.  (See -numcpu setting in dvid.go)
	MaxChunkHandlers = runtime.NumCPU()

	// HandlerToken is buffered channel to limit spawning of goroutines.
	// See ProcessChunk() in datatype/voxels for example.
	HandlerToken = make(chan int, MaxChunkHandlers)

	// Throttle allows server-wide throttling of operations.  This is used for voxels-based
	// compute-intensive operations on constrained servers.
	// TODO: This should be replaced with message queue mechanism for prioritized requests.
	Throttle = make(chan int, MaxThrottledOps)

	// SpawnGoroutineMutex is a global lock for compute-intense processes that want to
	// spawn goroutines that consume handler tokens.  This lets processes capture most
	// if not all available handler tokens in a FIFO basis rather than have multiple
	// concurrent requests launch a few goroutines each.
	SpawnGoroutineMutex sync.Mutex

	// Timeout in seconds for waiting to open a datastore for exclusive access.
	TimeoutSecs int

	// Keep track of the startup time for uptime.
	startupTime time.Time = time.Now()
)

func init() {
	// Initialize the number of throttled ops available.
	for i := 0; i < MaxThrottledOps; i++ {
		Throttle <- 1
	}

	// Initialize the number of handler tokens available.
	for i := 0; i < MaxChunkHandlers; i++ {
		HandlerToken <- 1
	}

	// Monitor the handler token load, resetting every second.
	loadCheckTimer := time.Tick(10 * time.Millisecond)
	ticks := 0
	go func() {
		for {
			<-loadCheckTimer
			ticks = (ticks + 1) % 100
			if ticks == 0 {
				ActiveHandlers = curActiveHandlers
				curActiveHandlers = 0
			}
			numHandlers := MaxChunkHandlers - len(HandlerToken)
			if numHandlers > curActiveHandlers {
				curActiveHandlers = numHandlers
			}
		}
	}()
}

// Initialize encapsulates platform-specific initialization functions and creates a public
// server.Context that provides logging and data persistence methods.
func Initialize(datastorePath, webAddress, webClientDir, rpcAddress string) error {
	// Setup logging

	// Setup storage tiers

	// Initialize datastore and set repo management as global var in package server
	ctx := datastore.Context{logger, metadata, smalldata, bigdata}
	var err error
	Repos, err = datastore.Initialize(ctx)
	if err != nil {
		return err
	}

	log.Printf("Using %d of %d logical CPUs for DVID.\n", dvid.NumCPU, runtime.NumCPU())

	// Register an error logger that appends to a file in this datastore directory.
	errorLog := filepath.Join(service.ErrorLogDir, ErrorLogFilename)
	file, err := os.OpenFile(errorLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Unable to open error logging file (%s): %s\n", errorLog, err.Error())
	}
	dvid.SetErrorLoggingFile(file)

	// Launch the web server
	go runningService.ServeHttp(webAddress, webClientDir)

	// Launch the rpc server
	err = runningService.ServeRpc(rpcAddress)
	if err != nil {
		log.Fatalln(err.Error())
	}

	return nil
}

// ---- Handle Storage Setup

func SetupEngines(path string, config dvid.Config) error {
	var err error
	var ok bool

	create := true
	Engines.kvEngine, err = local.NewKeyValueStore(path, create, config)
	if err != nil {
		return err
	}
	Engines.kvDB, ok = Engines.kvEngine.(OrderedKeyValueDB)
	if !ok {
		return fmt.Errorf("Database at %v is not a valid ordered key-value database", path)
	}
	Engines.kvSetter, ok = Engines.kvEngine.(OrderedKeyValueSetter)
	if !ok {
		return fmt.Errorf("Database at %v is not a valid ordered key-value setter", path)
	}
	Engines.kvGetter, ok = Engines.kvEngine.(OrderedKeyValueGetter)
	if !ok {
		return fmt.Errorf("Database at %v is not a valid ordered key-value getter", path)
	}

	Engines.graphEngine, err = NewGraphStore(path, create, config, Engines.kvDB)
	if err != nil {
		return err
	}
	Engines.graphDB, ok = Engines.graphEngine.(GraphDB)
	if !ok {
		return fmt.Errorf("Database at %v cannot support a graph database", path)
	}
	Engines.graphSetter, ok = Engines.graphEngine.(GraphSetter)
	if !ok {
		return fmt.Errorf("Database at %v cannot support a graph setter", path)
	}
	Engines.graphGetter, ok = Engines.graphEngine.(GraphGetter)
	if !ok {
		return fmt.Errorf("Database at %v cannot support a graph getter", path)
	}

	Engines.setup = true
	return nil
}

// --- Implement the three tiers of storage.
// --- In the case of a single local server with embedded storage engines, it's simpler
// --- because we don't worry about cross-process synchronization.

func SetupTiers() {
	MetaData = metaData{Engines.kvDB}
	SmallData = smallData{Engines.kvDB}
	BigData = bigData{Engines.kvDB}
}

// ---- Handle HTTP/RPC Setup

// VersionLocalID returns a server-specific local ID for the node with the given UUID.
func VersionLocalID(uuid dvid.UUID) (dvid.VersionLocalID, error) {
	if runningService.Service == nil {
		return 0, fmt.Errorf("Datastore service has not been started on this server.")
	}
	_, versionID, err := runningService.Service.LocalIDFromUUID(uuid)
	if err != nil {
		return 0, err
	}
	return versionID, nil
}

// --- Return datastore.Service and various database interfaces to support polyglot persistence --

// DatastoreService returns the current datastore service.  One DVID process
// is assigned to one datastore service, although it may be possible to have
// multiple (polyglot) persistence backends attached to that one service.
func DatastoreService() *datastore.Service {
	return runningService.Service
}

// KeyValueGetter returns the default service for retrieving key-value pairs.
func KeyValueGetter() (storage.KeyValueGetter, error) {
	if runningService.Service == nil {
		return nil, fmt.Errorf("No running datastore service is available.")
	}
	return runningService.KeyValueGetter()
}

// OrderedKeyValueDB returns the default ordered key-value database
func OrderedKeyValueDB() (storage.OrderedKeyValueDB, error) {
	if runningService.Service == nil {
		return nil, fmt.Errorf("No running datastore service is available.")
	}
	return runningService.OrderedKeyValueDB()
}

// OrderedKeyValueGetter returns the default service for retrieving ordered key-value pairs.
func OrderedKeyValueGetter() (storage.OrderedKeyValueGetter, error) {
	if runningService.Service == nil {
		return nil, fmt.Errorf("No running datastore service is available.")
	}
	return runningService.OrderedKeyValueGetter()
}

// OrderedKeyValueSetter returns the default service for storing ordered key-value pairs.
func OrderedKeyValueSetter() (storage.OrderedKeyValueSetter, error) {
	if runningService.Service == nil {
		return nil, fmt.Errorf("No running datastore service is available.")
	}
	return runningService.OrderedKeyValueSetter()
}

// GraphDB returns the default service for handling grah operations.
func GraphDB() (storage.GraphDB, error) {
	if runningService.Service == nil {
		return nil, fmt.Errorf("No running datastore service is available.")
	}
	return runningService.GraphDB()
}

// StorageEngine returns the default storage engine or nil if it's not available.
func StorageEngine() (storage.Engine, error) {
	if runningService.Service == nil {
		return nil, fmt.Errorf("No running datastore service is available.")
	}
	return runningService.StorageEngine(), nil
}

// Shutdown handles graceful cleanup of server functions before exiting DVID.
// This may not be so graceful if the chunk handler uses cgo since the interrupt
// may be caught during cgo execution.
func Shutdown() {
	if runningService.Service != nil {
		runningService.Service.Shutdown()
	}
	waits := 0
	for {
		active := MaxChunkHandlers - len(HandlerToken)
		if waits >= 20 {
			log.Printf("Already waited for 20 seconds.  Continuing with shutdown...")
			break
		} else if active > 0 {
			log.Printf("Waiting for %d chunk handlers to finish...\n", active)
			waits++
		} else {
			log.Println("No chunk handlers active...")
			break
		}
		time.Sleep(1 * time.Second)
	}
	storage.Shutdown()
	dvid.BlockOnActiveCgo()
}

// OpenDatastore returns a Server service.  Only one datastore can be opened
// for any server.
func OpenDatastore(datastorePath string) (service *Service, err error) {
	// Make sure we don't already have an open datastore.
	if runningService.Service != nil {
		err = fmt.Errorf("Cannot create new server. A DVID process can serve only one datastore.")
		return
	}

	// Get exclusive ownership of a DVID datastore
	log.Println("Getting exclusive ownership of datastore at:", datastorePath)

	var openErr *datastore.OpenError
	runningService.Service, openErr = datastore.Open(datastorePath)
	if openErr != nil {
		err = openErr
		return
	}
	runningService.ErrorLogDir = filepath.Dir(datastorePath)

	service = &runningService
	return
}

// StandaloneService adds logging and tailorable web addressing if we are running DVID on
// local servers instead of a managed cloud like Google.
type StandaloneService struct {
	Service

	// LogFile is the local file name for log output.
	LogFile string

	// The address for http server.
	WebAddress string
}

// Service holds what we need to run an http service using various storage engines.
type Service struct {
	// The currently opened DVID datastore
	*datastore.Service

	// The path to the DVID web client
	WebClientPath string

	// The address of the rpc server
	RPCAddress string
}

func (service *Service) sendContent(path string, w http.ResponseWriter, r *http.Request) {
	if len(path) > 0 && path[len(path)-1:] == "/" {
		path = filepath.Join(path, "index.html")
	}
	if service.WebClientPath == "" {
		if len(path) > 0 && path[0:1] == "/" {
			path = path[1:]
		}
		dvid.Log(dvid.Debug, "[%s] Serving from embedded files: %s\n", r.Method, path)

		resource := nrsc.Get(path)
		if resource == nil {
			http.NotFound(w, r)
			return
		}
		rsrc, err := resource.Open()
		if err != nil {
			BadRequest(w, r, err.Error())
			return
		}
		data, err := ioutil.ReadAll(rsrc)
		if err != nil {
			BadRequest(w, r, err.Error())
			return
		}
		dvid.SendHTTP(w, r, path, data)
	} else {
		// Use a non-embedded directory of files.
		filename := filepath.Join(runningService.WebClientPath, path)
		dvid.Log(dvid.Debug, "[%s] Serving from webclient directory: %s\n", r.Method, filename)
		http.ServeFile(w, r, filename)
	}
}

// Serve opens a datastore then creates both web and rpc servers for the datastore.
// This function must be called for DatastoreService() to be non-nil.
func (service *Service) Serve(webAddress, webClientDir, rpcAddress string) error {
	log.Printf("Using %d of %d logical CPUs for DVID.\n", dvid.NumCPU, runtime.NumCPU())

	// Register an error logger that appends to a file in this datastore directory.
	errorLog := filepath.Join(service.ErrorLogDir, ErrorLogFilename)
	file, err := os.OpenFile(errorLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Unable to open error logging file (%s): %s\n", errorLog, err.Error())
	}
	dvid.SetErrorLoggingFile(file)

	// Launch the web server
	go runningService.ServeHttp(webAddress, webClientDir)

	// Launch the rpc server
	err = runningService.ServeRpc(rpcAddress)
	if err != nil {
		log.Fatalln(err.Error())
	}

	return nil
}

// Listen and serve RPC requests using address.
func (service *Service) ServeRpc(address string) error {
	if address == "" {
		address = DefaultRPCAddress
	}
	service.RPCAddress = address
	dvid.Log(dvid.Debug, "Rpc server listening at %s ...\n", address)

	c := new(RPCConnection)
	rpc.Register(c)
	rpc.HandleHTTP()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	http.Serve(listener, nil)
	return nil
}