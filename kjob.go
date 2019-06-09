//////////////////////////////////////////////////////////////////////////////////
// kjob.go - ESI Job Management
//////////////////////////////////////////////////////////////////////////////////
//  kjobQueueInit():  Timer Init (called once from main)
//  gokjobQueueTick(t):  Timer tick function
//  newKjob(method, specnum, endpoint, entity, pages):  Initializes a new ESI Job, and adds it to the revolving door
//  kjob.beat():  Zombie Hunter- goal is for this to never be called.
//  kjob.print(string):  Universal "print all the things" function for debugging
//  kjob.start():  Brings kjob out of hibernation
//  kjob.stopJob():  Places kjob into hibernation
//  kjob.run():  Performs the next step, to process kjob
//  kjob.queuePages():  Adds all currently un-queued pages to the pages queue
//  kjob.requestHead():  Pulls a HEAD for the page, to identify expiration and pagecount
//  kjob.updateExp():  Centralized method for updating kjob expiration

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

var kjobStack = make(map[int]*kjob, 4096)
var kjobStackMutex = sync.RWMutex{}
var kjobQueueLen int
var kjobQueueTick *time.Ticker

type kjobQueueStruct struct {
	elements chan kjob
}

// initialize kjob queue ticker
func kjobQueueInit() {
	kjobQueueTick = time.NewTicker(500 * time.Millisecond) //500ms
	go gokjobQueueTick()
	log(nil, "kjob timer started")
}

func gokjobQueueTick() {
	for range kjobQueueTick.C {
		nowtime := ktime()
		kjobStackMutex.Lock()
		for itt := range kjobStack {
			if kjobStack[itt].running == false && kjobStack[itt].NextRun < nowtime {
				go kjobStack[itt].start()
			}
		}
		kjobStackMutex.Unlock()
	}
}

type kjob struct {

	// "read-only" variables (not mutex'd, only written by newKjob())
	Method    string `json:"method"` //Method: 'get', 'put', 'update', 'delete'
	Source    string `json:"source"` //Entity: "region_id": "10000002", "character_id": "1120048880"
	Owner     string `json:"owner"`
	URL       string `json:"url"` //URL: concat of Spec and Endpoint, with entity processed
	CI        string `json:"ci"`  //CI: concat of endpoint:JSON.Marshall(entity)
	spec      specS
	Token     string `json:"token"` //evesso access_token
	RunTag    int64  `json:"runTag"`
	InsLength int    `json:"insLength"`
	IDLength  int    `json:"idLength"`

	NextRun   int64   `json:"nextRun"`
	Expires   int64   `json:"expires"`    //milliseconds since 1970, when cache miss will occur
	ExpiresIn float64 `json:"expires_in"` //milliseconds until expiration, at completion of first page (or head) pulled

	APICalls  uint16 `json:"apiCalls"`  //count of API Calls (including head, errors, etc)
	APICache  uint16 `json:"apiCache"`  //count of 304 responses received
	APIErrors uint16 `json:"apiErrors"` //count of >304 statuses received

	BytesDownloaded int `json:"bytesDownloaded"` //total bytes downloaded
	BytesCached     int `json:"bytesCached"`     //total bytes cached

	PullType       uint16 `json:"pullType"`
	Pages          uint16 `json:"pages"`          //Pages: 0, 1
	PagesProcessed uint16 `json:"pagesProcessed"` //count of completed pages (excluding heads)
	PagesQueued    uint16 `json:"pagesQueued"`    //cumlative count of pages queued

	Records      int64 `json:"records"`      //cumlative count of records
	AffectedRows int64 `json:"affectedRows"` //cumlative count of affected records
	RemovedRows  int64 `json:"removedRows"`  //cumlative count of removed records

	heart    *time.Timer   //heartbeat timer
	req      *http.Request //http request
	running  bool
	jobMutex sync.RWMutex
	//	jobMutexLockedBy string
	//	jobMutexLocked   bool
	page  map[uint16]*kpage // hashmap of [pagenumber]*kpage for all child elements
	table *table
}

func newKjob(method string, specnum string, endpoint string, entity map[string]string, pages uint16, table *table) {
	tspec := getSpec(method, specnum, endpoint)
	if tspec.invalid {
		log(nil, "Invalid job received: SPEC invalid")
		return
	}
	var sentity, owner string
	var ok bool
	if sentity, ok = entity["region_id"]; !ok {
		if sentity, ok = entity["structure_id"]; !ok {
			if sentity, ok = entity["character_id"]; !ok {
				log(nil, "Invalid job received: unable to resolve entity")
				return
			}
			owner = sentity
		} else {
			owner = "NULL"
		}
	} else {
		owner = "NULL"
	}

	ciString, _ := json.Marshal(entity)
	tmp := kjob{
		Method:   method,
		Source:   sentity,
		Owner:    owner,
		Token:    "none",
		Pages:    pages,
		PullType: pages,
		URL:      fmt.Sprintf("%s%s?datasource=tranquility", specnum, endpoint),
		heart:    time.NewTimer(30 * time.Second),
		CI:       fmt.Sprintf("%s|%s", endpoint, ciString),
		spec:     tspec,
		jobMutex: sync.RWMutex{},
		page:     make(map[uint16]*kpage),
		table:    table}
	for se, sed := range entity {
		tmp.URL = strings.Replace(tmp.URL, "{"+se+"}", sed, -1)
	}

	go func() {
		for range tmp.heart.C {
			tmp.beat()
		}
	}()
	kjobStackMutex.Lock()
	kjobStack[kjobQueueLen] = &tmp
	kjobQueueLen++
	kjobStackMutex.Unlock()
}
func (k *kjob) LockJob() {
	// if k.jobMutexLocked {
	// foop:
	// 	time.Sleep(1000 * time.Millisecond)
	// 	if k.jobMutexLocked {
	// 		log(k.CI, "ERR: Job Mutex has been locked for >100ms by "+k.jobMutexLockedBy)
	// 		goto foop
	// 	}
	// }
	// pc, fn, line, _ := runtime.Caller(1)
	// justfn := strings.Split(fn, "/")
	k.jobMutex.Lock()
	// k.jobMutexLocked = true
	// k.jobMutexLockedBy = fmt.Sprintf("%.3f [%s(%s):%d]", float64(ktime())/1000, justfn[len(justfn)-1], runtime.FuncForPC(pc).Name(), line)
}
func (k *kjob) UnlockJob() {
	// k.jobMutexLocked = false
	// k.jobMutexLockedBy = ""
	k.jobMutex.Unlock()
}

// returns all Finished Cached (304) Pages' InsIds, and clears InsIds
func (k *kjob) getCachedInsIds() string {
	var b strings.Builder
	comma := ""
	for it := range k.page {
		k.page[it].pageMutex.Lock()
		if k.page[it].InsReady && (k.page[it].Ins.Len() == 0) && (k.page[it].InsIds.Len() > 0) {
			fmt.Fprintf(&b, "%s%s", comma, k.page[it].InsIds.String())
			k.page[it].InsIds.Reset()
			comma = ","
		}
		k.page[it].pageMutex.Unlock()
	}
	return b.String()
}

// returns all Finished (200) Pages' Ins, and clears Ins
func (k *kjob) getIns() string {
	var b strings.Builder
	comma := ""
	for it := range k.page {
		k.page[it].pageMutex.Lock()
		if k.page[it].InsReady && (k.page[it].Ins.Len() > 0) {
			fmt.Fprintf(&b, "%s%s", comma, k.page[it].Ins.String())
			k.page[it].Ins.Reset()
			comma = ","
		}
		k.page[it].pageMutex.Unlock()
	}
	return b.String()
}
func (k *kjob) beat() {
	k.print("ZOMBIE")
	k.LockJob()
	k.stopJob(true)
	k.UnlockJob()
	k.start()
}
func (k *kjob) print(msg string) {
	log(msg, fmt.Sprintf("%s %s %d %s %s %d %d %.0f %d %d %d %d %d %d %d %d %d %d %d",
		k.Method, k.CI,
		k.spec.cache, k.spec.security, k.Token,
		k.NextRun, k.Expires, k.ExpiresIn,
		k.APICalls, k.APICache, k.APIErrors,
		k.BytesDownloaded, k.BytesCached,
		k.PullType, k.Pages, k.PagesProcessed, k.PagesQueued,
		kjobQueueLen, getMetric(k.CI)))
}
func (k *kjob) start() {
	k.LockJob()
	k.heart.Reset(30 * time.Second)
	k.RunTag = ktime()
	k.running = true
	addMetric(k.CI)
	k.UnlockJob()
	k.run()
}
func (k *kjob) stopJob(zombie bool) {
	//todo: final sql write, store metrics
	//k.mutex.Lock() **Should be locked in a wrapper around the call to this.
	log(k.CI, fmt.Sprintf("%t Pages:%d/%d Cached:%db DL:%db Records:%d Affected:%d Removed:%d ETRO: %dms", zombie, k.PagesProcessed, k.Pages, k.BytesCached, k.BytesDownloaded,
		k.Records, k.AffectedRows, k.RemovedRows, k.Expires-ktime()))
	k.heart.Stop()
	for it := range k.page {
		k.page[it].destroy()
	}
	k.running = false
	k.Token = "none"
	k.Expires = 0
	k.APICalls = 0
	k.APICache = 0
	k.APIErrors = 0
	k.BytesDownloaded = 0
	k.BytesCached = 0
	k.Pages = k.PullType
	k.PagesProcessed = 0
	k.PagesQueued = 0
	k.Records = 0
	k.AffectedRows = 0
	k.RemovedRows = 0
	//k.mutex.Unlock()
}
func (k *kjob) run() {
	//k.heart.Reset(30 * time.Second)
	if k.spec.security != "" && len(k.Token) < 5 {
		log(k.CI, "todo: get token.")
		return
	}
	if k.Pages == 0 {
		go k.requestHead()
		return
	}
}
func (k *kjob) queuePages() {
	k.LockJob()
	//log("kjob.go:k.queuePages()", fmt.Sprintf("%s Queueing pages %d to %d", k.CI, k.PagesQueued+1, k.Pages))
	for i := k.PagesQueued + 1; i <= k.Pages; i++ {
		k.newPage(i)
	}
	k.UnlockJob()
}
func (k *kjob) requestHead() {
	//log("kjob.go:k.requestHead("+k.CI+")", "requesting head "+k.CI)
	addMetric("HEAD:" + k.CI)
	//k.heart.Reset(30 * time.Second)
	k.APICalls++
	if backoff {
		time.Sleep(5 * time.Second)
		go k.requestHead()
		return
	}
	req, err := http.NewRequest("HEAD", c.EsiURL+k.URL, nil)
	if err != nil {
		log(k.CI, err)
		return
	}
	k.req = req
	if k.spec.security != "" && len(k.Token) > 5 {
		k.req.Header.Add("Authorization", "Bearer "+k.Token)
	}
	resp, err := client.Do(k.req)
	if err != nil {
		log(k.CI, err)
		return
	}
	/*
				TODO: re-add error_limit/backoff
				      if (this.response_headers['x-esi-error-limit-remain'] && (this.response_headers[':status'] > 399)) {
		        error_remain = this.response_headers['x-esi-error-limit-remain'];
		        clearTimeout(error_reset_timer);
		        error_reset_timer = setTimeout(() => { error_remain = 100; }, (parseInt(this.response_headers['x-esi-error-limit-reset']) * 1000));

		        if (error_remain < 30) {
		          console.log("Backing off!");
		          backoff = true;
		          setTimeout(() => { backoff = false; console.log("Resuming..."); }, 20000);
		        }

					}
	*/
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		k.updateExp(resp.Header["Expires"][0])

		var timepct float64
		if k.ExpiresIn > 0 {
			timepct = 100 * (float64(k.ExpiresIn) / float64(k.spec.cache*1000))
		} else {
			timepct = 0
		}
		if timepct < c.MinCachePct {
			log(k.CI, " expires too soon, recycling!")
			k.LockJob()
			k.stopJob(false)
			k.UnlockJob()
			return
		}
		if pgs, ok := resp.Header["X-Pages"]; ok {
			k.updatePageCount(pgs[0])
		}
	}
}

//"2006-01-02T15:04:05Z"
//"Mon, 02 Jan 2006 15:04:05 MST"
func (k *kjob) updateExp(expire string) {
	exp, err := time.Parse("Mon, 02 Jan 2006 15:04:05 MST", expire)
	if err == nil {
		k.LockJob()
		k.Expires = int64(exp.UnixNano() / int64(time.Millisecond))
		k.ExpiresIn = float64(k.Expires - ktime())
		k.NextRun = k.Expires + 750
		k.UnlockJob()
	}
}
func (k *kjob) updatePageCount(pages string) {
	pgss, _ := strconv.Atoi(pages)
	if k.Pages != uint16(pgss) {
		k.LockJob()
		k.Pages = uint16(pgss)
		k.UnlockJob()
		if k.Pages != k.PagesQueued {
			k.queuePages()
		}
	}
}
func (k *kjob) processPage() {
	k.LockJob()
	defer k.UnlockJob()
	k.PagesProcessed++
	pagesFinished++

	//update last_seen on cached entries if
	//  15000+ cached ids ready, or
	//  non-zero cached ids ready, and this is the last page
	if (k.IDLength >= 15000) || ((k.IDLength > 0) && k.PagesProcessed == k.Pages) {

		cachedInsIds := k.getCachedInsIds()
		if len(cachedInsIds) == 0 {
			log(nil, "Memory read error")
			k.stopJob(false)
			return
		}
		query := fmt.Sprintf("UPDATE `%s`.`%s` SET last_seen=%d WHERE %s IN (%s)", k.table.database, k.table.name, k.RunTag, k.table.primaryKey, cachedInsIds)
		k.Records += safeQuery(query)
		k.IDLength = 0
	}

	//insert records if:
	//  15000+ records ready, or
	//  non-zero records ready, and this is the last page
	if (k.InsLength >= 15000) || ((k.InsLength > 0) && k.PagesProcessed == k.Pages) {
		getIds := k.getIns()
		if len(getIds) == 0 {
			log(nil, "Memory read error")
			k.stopJob(false)
			return
		}
		query := fmt.Sprintf("INSERT INTO `%s`.`%s` (%s) VALUES %s %s", k.table.database, k.table.name, k.table.columnOrder(), getIds, k.table.duplicates)
		tmp := safeQuery(query)
		k.Records += tmp
		k.AffectedRows += tmp
		k.InsLength = 0
	}

	if k.PagesProcessed == k.Pages {
		k.RemovedRows += k.table.handleEndGood(k.table, k)
		k.stopJob(false)
	}
}
