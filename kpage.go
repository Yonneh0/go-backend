//////////////////////////////////////////////////////////////////////////////////
// kpage.go - ESI Page Loading
//////////////////////////////////////////////////////////////////////////////////
//  kpageQueueS.Push(element):  Adds element to the FIFO queue
//  kpageQueueS.Pop():  Returns the oldest element from the queue
//  gokpageQueueTick(t):  Timer tick function
//  kpageQueueInit(): Timer/Queue Init (called once from main)
//  kjob.newPage(page): Queues page on behalf of kjob
//  curInFlightmm(): Silly Mechanics (defer cal for decrementing curInFlight)
//  kpage.requestPage(): Launches kpage request

package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

var kpageQueue *kpageQueueS
var kpageQueueTick *time.Ticker
var kpageQueueMutex sync.Mutex

var errorRemain uint32 = 100
var errorResetTimer *time.Timer

var queueTicker *time.Ticker

var backoff = false
var backoffTimer *time.Timer

var curInFlight = 0
var lastInFlight = 0
var inFlight = make(map[int]*kpage, c.MaxInFlight)
var inFlightMutex sync.Mutex
var pagesFired = 0
var lastFired = 0

var pagesFinished = 0
var lastFinished = 0

var bCached = 0
var bDownload = 0

type kpageQueueS struct {
	elements chan *kpage
	len      int
}
type kpage struct {
	job       *kjob
	page      uint16
	cip       string
	body      []byte
	req       *http.Request
	running   int64
	dead      bool
	pageMutex sync.Mutex
	ids       strings.Builder
	ins       strings.Builder
	upd       strings.Builder
	recs      int64
	insrecs   int64
	updrecs   int64
}

func (kpageQueueS *kpageQueueS) Push(element *kpage) {
	kpageQueueMutex.Lock()
	defer kpageQueueMutex.Unlock()
	select {
	case kpageQueueS.elements <- element:
		kpageQueueS.len++
	default:
		panic("Queue full")
	}
}
func (kpageQueueS *kpageQueueS) Pop() *kpage {
	kpageQueueMutex.Lock()
	defer kpageQueueMutex.Unlock()
	select {
	case e := <-kpageQueueS.elements:
		kpageQueueS.len--
		return e
	default:
		panic("Queue empty")
	}
}
func gokpageQueueTick() {
	for range kpageQueueTick.C {
		kpageQueueMutex.Lock()
		kpageQueueLen := kpageQueue.len
		kpageQueueMutex.Unlock()

		for kpageQueueLen > 0 && (curInFlight < c.MaxInFlight) && !backoff {
			qitem := kpageQueue.Pop()
			if qitem.dead == false {
				err := -1
				inFlightMutex.Lock()
				for it := 0; it < c.MaxInFlight; it++ {
					if inFlight[it].dead {
						err = it
						break
					}
				}
				curInFlight++
				pagesFired++
				inFlight[err] = qitem
				inFlight[err].running = ktime()
				go inFlight[err].requestPage()
				inFlightMutex.Unlock()
			} else {
				// log("kpage.go:gokpageQueueTick()", fmt.Sprintf("Encountered dead cip in queue %s", qitem.cip))
			}
			kpageQueueMutex.Lock()
			kpageQueueLen = kpageQueue.len
			kpageQueueMutex.Unlock()
		}
	}
}

//initialize kpage queue tickers
func kpageInit() {

	kpageQueue = &kpageQueueS{
		elements: make(chan *kpage, 8192),
	}

	kpageQueueTick = time.NewTicker(10 * time.Millisecond)
	go gokpageQueueTick()

	for i := 0; i < c.MaxInFlight; i++ {
		inFlight[i] = &kpage{dead: true}
	}

	queueTicker = time.NewTicker(1 * time.Second)
	go queueLog()

	log(nil, "kpage tickers started")
}

func queueLog() {
	for range queueTicker.C {
		kpageQueueMutex.Lock()
		fff := kpageQueue.len
		kpageQueueMutex.Unlock()
		if fff > 0 || lastFinished != pagesFinished || lastFired != pagesFired || lastInFlight != curInFlight {
			timenow := ktime()
			lastFinished = pagesFinished
			lastFired = pagesFired
			lastInFlight = curInFlight
			entry := fmt.Sprintf("%12d/%12d Q:%6d Fired:%6d Done:%6d Hot(%3d of %3d) ", bCached, bDownload, fff, lastFired, lastFinished, lastInFlight, c.MaxInFlight)
			// fntry := ""
			gntry := ""
			// hntry := ""
			inFlightMutex.Lock()
			for it := 0; it < c.MaxInFlight; it++ {
				// fntry = fmt.Sprintf("%s%12s", fntry, inFlight[it].whatDo)
				// hntry = fmt.Sprintf("%s%11d ", hntry, inFlight[it].job.entity)
				if inFlight[it].dead {
					gntry = gntry + "**** "
				} else {
					gntry = fmt.Sprintf("%s%4d ", gntry, timenow-inFlight[it].running)
				}
			}
			inFlightMutex.Unlock()
			log("<QUEUE>", entry+gntry)
			// log("       ", fntry)
			// log("       ", gntry)
			// log("       ", hntry)
		}
	}
}

func (k *kjob) newPage(page uint16, requeue bool) {
	if requeue {
		k.jobMutex.Lock()
		defer k.jobMutex.Unlock()
	}
	k.page[page] = &kpage{
		job:  k,
		page: page,
		cip:  fmt.Sprintf("%s|%d", k.CI, page),
	}
	kpageQueue.Push(k.page[page])
}
func (k *kpage) destroy() {
	if k.running > 0 {
		curInFlight--
		k.running = 0
	}
	k.ins.Reset()
	k.ids.Reset()
	k.upd.Reset()
	k.dead = true
}
func (k *kpage) requestPage() {
	var err error
	defer k.destroy()
	if k.dead {
		return
	}

	if backoff {
		log(k.cip, "backoff trigger")
		k.job.stopJob(true)
		return
	}

	k.req, err = http.NewRequest(strings.ToUpper(k.job.Method), c.EsiURL+k.job.URL+"&page="+strconv.Itoa(int(k.page)), nil)
	if err != nil {
		log(k.cip, err)
		k.job.forceNextRun(86400000)
		k.job.stopJob(true)
		return
	}

	etaghdr := getEtag(k.cip)
	if len(etaghdr) > 0 {
		k.req.Header.Add("If-None-Match", etaghdr)
	}
	if k.job.spec.security != "" && len(k.job.Token) > 5 {
		k.req.Header.Add("Authorization", "Bearer "+k.job.Token)
	}
	resp, err := client.Do(k.req)
	if err != nil {
		log(k.cip, err)
		k.job.newPage(k.page, true)
		return
	}

	defer resp.Body.Close()
	if k.dead {
		return
	}

	//extract headers
	// errorlimitremain, okerrorlimitremain := resp.Header["x-esi-error-limit-remain"]
	// errorlimitreset, okerrorlimitreset := resp.Header["x-esi-error-limit-reset"]

	// if okerrorlimitremain && okerrorlimitreset && resp.StatusCode > 399 {
	/*
			errt, _ := strconv.Atoi(errorlimitreset[0])
			errr, _ := strconv.Atoi(errorlimitremain[0])
			atomic.StoreUint32(&errorRemain, uint32(errr))
			errorResetTimer.Reset(time.Duration(errt) * time.Second) //TODO: add goroutine that watches this timer, and resets to 100

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

	//k.job.heart.Reset(30 * time.Second)

	if resp.StatusCode == 200 {
		var err error
		k.body, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			log(k.cip, err)
			k.job.newPage(k.page, true)
			return
		}
		if err = k.job.table.handlePageData(k); err != nil {
			log(k.cip, err)
			k.job.newPage(k.page, true)
			return
		}

		k.job.jobMutex.Lock()
		k.pageMutex.Lock()
		if k.insrecs > 0 {
			if k.job.RecordsIns > 0 {
				k.job.ins.WriteString(",")
			}
			k.job.ins.WriteString(k.ins.String())

			k.job.RecordsIns += k.insrecs
		}
		if k.updrecs > 0 {
			if k.job.RecordsUpd > 0 {
				k.job.upd.WriteString(",")
			}
			k.job.upd.WriteString(k.upd.String())
			k.job.RecordsUpd += k.updrecs
		}
		k.job.jobMutex.Unlock()
		k.pageMutex.Unlock()

		if etag, ok := resp.Header["Etag"]; ok {
			setEtag(k.cip, etag[0], k.ids.String(), len(k.body))
		}
		k.job.jobMutex.Lock()
		bDownload += len(k.body)
		k.job.BytesDownloaded += len(k.body)
		k.job.jobMutex.Unlock()
	} else if resp.StatusCode == 304 {
		ids, length := getEtagIds(k.cip)
		if length == 0 {
			killEtag(k.cip)
			log(k.cip, "No Data returned!")
			k.job.newPage(k.page, true)
			return
		}
		k.ids.WriteString(ids)
		if k.ids.Len() > 0 {
			if err = k.job.table.handlePageCached(k); err != nil {
				killEtag(k.cip)
				log(k.cip, err)
				k.job.newPage(k.page, true)
				return
			}
		}
		k.job.jobMutex.Lock()
		bCached += length
		k.job.BytesCached += length
		k.job.jobMutex.Unlock()
	}

	if resp.StatusCode == 200 || resp.StatusCode == 304 {
		if exp, ok := resp.Header["Expires"]; ok {
			k.job.updateExp(exp[0])
		}
		if pgs, ok := resp.Header["X-Pages"]; ok {
			k.job.updatePageCount(pgs[0])
		}

		k.job.jobMutex.Lock()
		k.job.Records += k.recs
		k.job.jobMutex.Unlock()
		if k.dead {
			return
		}

		k.job.processPage()
	} else {
		log(k.cip, fmt.Sprintf("RCVD (%d) %s(%d of %d) %s&page=%d %db", resp.StatusCode, k.job.Method, k.page, k.job.Pages, k.job.URL, k.page, len(k.body)))
		k.job.newPage(k.page, true)
	}
}
