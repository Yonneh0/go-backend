// misc methods used throughout

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
)

// configuration file path
var configFile = "./config.json"

// array of all eve regions for testing
var eveRegions = []int{10000001, 10000002, 10000003, 10000004, 10000005, 10000006, 10000007, 10000008, 10000009, 10000010, 10000011, 10000012, 10000013, 10000014, 10000015, 10000016, 10000017, 10000018, 10000019, 10000020, 10000021, 10000022, 10000023, 10000025, 10000027, 10000028, 10000029, 10000030, 10000031, 10000032, 10000033, 10000034, 10000035, 10000036, 10000037, 10000038, 10000039, 10000040, 10000041, 10000042, 10000043, 10000044, 10000045, 10000046, 10000047, 10000048, 10000049, 10000050, 10000051, 10000052, 10000053, 10000054, 10000055, 10000056, 10000057, 10000058, 10000059, 10000060, 10000061, 10000062, 10000063, 10000064, 10000065, 10000066, 10000067, 10000068, 10000069}

// global http client for esi access
var client *http.Client

// global configuration struct
var c conf

// centralized log function, so all output can be piped to other places
func log(message ...interface{}) {
	pc, fn, line, _ := runtime.Caller(1)
	justfn := strings.Split(fn, "/")
	var b strings.Builder
	fmt.Fprintf(&b, "")
	for it := range message {
		fmt.Fprintf(&b, " %v", message[it])
	}
	fmt.Printf("%.3f [%s(%s):%d]%s\n", float64(ktime())/1000, justfn[len(justfn)-1], runtime.FuncForPC(pc).Name(), line, b.String())
}

// centralized log function, so all output can be piped to other places
func logf(format string, a ...interface{}) {
	pc, fn, line, _ := runtime.Caller(1)
	justfn := strings.Split(fn, "/")
	out := fmt.Sprintf(format, a...)
	fmt.Printf("%.3f [%s(%s):%d] %s\n", float64(ktime())/1000, justfn[len(justfn)-1], runtime.FuncForPC(pc).Name(), line, out)
}

// centralized log function, so all output can be piped to other places
func logq(message string) {
	fmt.Printf("%.3f %s\n", float64(ktime())/1000, message)
}

// time format used throughout
func ktime() int64 { return time.Now().UnixNano() / int64(time.Millisecond) }

func safeMove(src string, dst string) {
	os.Remove(dst + ".bak")
	os.Rename(dst, dst+".bak")
	if err := os.Rename(src, dst); err != nil {
		log(err)
		return
	}
	os.Remove(dst + ".bak")
}

// read config.json file
func initConfig() {
	jsonFile, err := os.Open(configFile)
	if err != nil {
		log(err)
		panic(err)
	}
	defer jsonFile.Close()
	byteValue, errr := ioutil.ReadAll(jsonFile)
	if errr != nil {
		log(err)
		panic(err)
	}
	if err := json.Unmarshal(byteValue, &c); err != nil {
		log(err)
		panic(err)
	}
	logf("Read %db from %s", len(byteValue), configFile)
	byteValue = nil
}

type conf struct {
	EsiURL              string            `json:"esi_url"`
	Domain              string            `json:"domain"`
	Email               string            `json:"email"`
	MaxInFlight         uint64            `json:"max_in_flight"`
	MinCachePct         float64           `json:"min_cache_pct"`
	BackoffThreshold    int               `json:"backoff_threshold"`
	BackoffSeconds      time.Duration     `json:"backoff_seconds"`
	ErrDisableThreshold int64             `json:"err_disable_threshold"`
	SQL                 map[string]string `json:"sql"`
	Oauth               map[string]oauth  `json:"oauth"`
	Tables              map[string]*table `json:"tables"`
}

// initialize http global
func initClient() {
	client = &http.Client{
		Transport: &http2.Transport{},
		Timeout:   7 * time.Second,
	}
}

type oauth struct {
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`
	Callback     string `json:"callback"`
	Redirect     string `json:"redirect"`
	AuthURL      string `json:"authURL"`
	RefererURL   string `json:"refererURL"`
	TokenURL     string `json:"tokenURL"`
	VerifyURL    string `json:"verifyURL"`
	RevokeURL    string `json:"revokeURL"`
	APIBase      string `json:"apiBase"`
}

type specS struct {
	invalid  bool
	security string
	cache    int
	items    int
	paged    bool
}

// get the stored spec information from SQL
func getSpec(method string, specnum string, endpoint string) specS {
	row := database.QueryRow(fmt.Sprintf("SELECT security,cache,items,paged FROM `%s`.`%s` WHERE method=? AND spec=? AND endpoint=?", c.Tables["spec"].DB, c.Tables["spec"].Name), method, specnum, endpoint)
	var specc specS
	err := row.Scan(&specc.security, &specc.cache, &specc.items, &specc.paged)
	if err != nil {
		specc.invalid = true
		return specc
	}
	return specc
}

// debugging replacement for sync.Mutex that does more than block for ever. Do not use in production, it is VERY slow.
type debugOnlyMutex struct {
	lock       uint32
	lockByFile string
	lockByFunc string
	lockByLine int
	lockByTime int64
}

// (standin for sync.Mutex.Lock) Lock locks m. If the lock is already in use, it will periodically return diagnostic messages to stdout
func (debugOnlyMutex *debugOnlyMutex) Lock() {
	var ms uint32
	var it uint32 = 1
	var contested bool
	stime := time.Now().UnixNano()
	for !atomic.CompareAndSwapUint32(&debugOnlyMutex.lock, 0, 1) {
		time.Sleep(5 * time.Millisecond)
		ms++
		if ms > 250*it {
			contested = true
			ms = 0
			it++
			ctime := time.Now().UnixNano()
			pc, fn, line, _ := runtime.Caller(1)
			justfn := strings.Split(fn, "/")

			fmt.Printf("%.3f [%s(%s):%d] Requested Lock %dms ago, Locked by [%s(%s):%d] %dms ago\n",
				float64(ctime)/float64(time.Second),
				justfn[len(justfn)-1], runtime.FuncForPC(pc).Name(), line,
				int((ctime-stime)/int64(time.Millisecond)),
				debugOnlyMutex.lockByFile, debugOnlyMutex.lockByFunc, debugOnlyMutex.lockByLine,
				int((ctime-debugOnlyMutex.lockByTime)/int64(time.Millisecond)),
			)
		}
	}
	pc, fn, line, _ := runtime.Caller(1)
	justfn := strings.Split(fn, "/")

	if contested {
		ctime := time.Now().UnixNano()
		fmt.Printf("%.3f [%s(%s):%d] Contested Lock freed after %dms by [%s(%s):%d]\n",
			float64(ctime)/float64(time.Second),
			justfn[len(justfn)-1], runtime.FuncForPC(pc).Name(), line,
			int((ctime-stime)/int64(time.Millisecond)),
			debugOnlyMutex.lockByFile, debugOnlyMutex.lockByFunc, debugOnlyMutex.lockByLine,
		)
	}
	debugOnlyMutex.lockByFile = justfn[len(justfn)-1]
	debugOnlyMutex.lockByFunc = runtime.FuncForPC(pc).Name()
	debugOnlyMutex.lockByLine = line
	debugOnlyMutex.lockByTime = time.Now().UnixNano()

}

// (standin for sync.Mutex.Unlock) unlocks debugOnlyMutex. If the mutex is already unlocked, it prints a descriptive message to stdout
func (debugOnlyMutex *debugOnlyMutex) Unlock() {
	if !atomic.CompareAndSwapUint32(&debugOnlyMutex.lock, 1, 0) {
		ctime := time.Now().UnixNano()
		pc, fn, line, _ := runtime.Caller(1)
		justfn := strings.Split(fn, "/")
		fmt.Printf("%.3f [%s(%s):%d] Requested Unlock of an unlocked mutex\n",
			float64(ctime)/float64(time.Second),
			justfn[len(justfn)-1], runtime.FuncForPC(pc).Name(), line,
		)
	}
}

func max(v1 int64, v2 int64) int64 {
	if v1 > v2 {
		return v1
	}
	return v2
}
func min(v1 int64, v2 int64) int64 {
	if v1 < v2 {
		return v1
	}
	return v2
}
func byt(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%db", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%s%cb", bytFormat(float64(b)/float64(div)), "kmgtpe"[exp])
}
func bytn(b uint64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%s%c", bytFormat(float64(b)/float64(div)), "kmgtpe"[exp])
}
func bytFormat(num float64) string {
	if num < 9.5 {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", num), "0"), ".")
	}
	if num < 99.5 {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", num), "0"), ".")
	}
	return fmt.Sprintf("%.0f", num)

}
