package stats

import (
	"log/slog"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	listenerTimeout = 10 * time.Second
)

func createTestStatsManager() *StatsManager {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	return NewStatsManager(logger, listenerTimeout)
}

func TestEnqueue(t *testing.T) {
	sm := createTestStatsManager()

	sm.Enqueue(&StatusUpdate{})
	length := len(sm.Updates.Queue)
	assert.Equal(t, 1, length, "Update was not added properly")
}

func TestEnqueue_Multiple(t *testing.T) {
	sm := createTestStatsManager()

	count := 10000
	wg := sync.WaitGroup{}
	for i := 1; i <= count; i++ {
		wg.Add(1)
		go func() {
			time.Sleep(100 * (time.Duration(rand.Intn(1000000))))
			sm.Enqueue(&StatusUpdate{})
			wg.Done()
		}()
	}
	wg.Wait()

	length := len(sm.Updates.Queue)

	assert.Equal(t, count, length, "Update was not added properly")
}

func TestDequeue(t *testing.T) {
	sm := createTestStatsManager()

	suIn := &StatusUpdate{}

	sm.Enqueue(&StatusUpdate{})
	length := len(sm.Updates.Queue)
	assert.Equal(t, 1, length, "Update was not added properly")

	suOut := sm.dequeue()
	assert.Equal(t, suIn, suOut, "Update was not dequeued properly")
	_ = sm.dequeue()
}

func TestDequeue_Multiple(t *testing.T) {
	sm := createTestStatsManager()

	var arrIn []*StatusUpdate
	count := 101 //CAREFUL: Only works for uneven numbers

	for i := 1; i <= count; i++ {
		var su *StatusUpdate
		if i%2 == 0 {
			su = &StatusUpdate{Status: StatusSuccess}
		} else {
			su = &StatusUpdate{Status: StatusFailed}
		}
		arrIn = append(arrIn, su)
		sm.Enqueue(su)
	}

	var arrOut []*StatusUpdate
	for i := 1; i <= count; i++ {
		out := sm.dequeue()
		arrOut = append(arrOut, out)
	}

	assert.Equal(t, len(arrOut), len(arrIn), "Input and Output have different number of elements")

	b := true

	for i := 0; i <= len(arrIn)-1; i++ {
		if *arrIn[i] != *arrOut[len(arrOut)-(i+1)] {
			b = false
		}
	}

	assert.True(t, b, "Input and output do not contain same elements")
}

func TestAddListener(t *testing.T) {
	sm := createTestStatsManager()

	nodeID := "1"
	listeningChan := make(chan StatusUpdate)

	sm.AddListener(nodeID, listeningChan)

	b1 := sm.listeners[nodeID] != nil
	b2 := sm.listeners["sddwdw"] != nil

	assert.True(t, b1)
	assert.False(t, b2)
}

func TestAddListeners_Multiple(t *testing.T) {
	sm := createTestStatsManager()

	count := 200
	wg := sync.WaitGroup{}
	m := make(map[string]chan StatusUpdate)

	for i := 1; i <= count-1; i++ {
		wg.Add(1)
		s := strconv.Itoa(i)
		ch := make(chan StatusUpdate)
		m[s] = ch
		go func(id string, ch chan StatusUpdate) {
			time.Sleep(100 * (time.Duration(rand.Intn(1000000))))
			sm.AddListener(id, ch)
			wg.Done()
		}(s, ch)
	}
	wg.Wait()

	b := true

	for key, value := range m {
		val, ok := sm.listeners[key]
		if !(ok && val == value) {
			b = false
		}
	}

	assert.True(t, b, "Not all listeners where recorded correctly")
}

func TestRemoveListener(t *testing.T) {
	sm := createTestStatsManager()

	nodeID := "1"
	listeningChan := make(chan StatusUpdate)

	sm.AddListener(nodeID, listeningChan)

	b1 := sm.listeners[nodeID] != nil

	assert.True(t, b1)

	sm.RemoveListener(nodeID)

	b2 := sm.listeners[nodeID] != nil

	assert.False(t, b2, "Listener was not removed properly")
}

func TestRemoveListener_Multiple(t *testing.T) {
	sm := createTestStatsManager()

	var arrID []string
	count := 200

	for i := 1; i <= count-1; i++ {
		s := strconv.Itoa(i)
		arrID = append(arrID, s)
		ch := make(chan StatusUpdate)
		sm.AddListener(s, ch)
	}

	wg := sync.WaitGroup{}

	for i := 1; i <= count-1; i++ {
		wg.Add(1)
		s := strconv.Itoa(i)
		go func(id string) {
			time.Sleep(100 * (time.Duration(rand.Intn(1000000))))
			sm.RemoveListener(id)
			wg.Done()
		}(s)
	}
	wg.Wait()

	i := 0

	for _, _ = range sm.listeners {
		i++
	}

	assert.Equal(t, 0, i, "Not all listeners where removed")
}

func TestGetListenerByID_Multiple(t *testing.T) {
	sm := createTestStatsManager()

	count := 200

	for i := 1; i <= count-1; i++ {
		s := strconv.Itoa(i)
		ch := make(chan StatusUpdate)
		sm.AddListener(s, ch)
	}

	b := true

	for i := 1; i <= count-1; i++ {
		ch := sm.GetListenerByID(strconv.Itoa(i))
		if ch == nil {
			b = false
		}
	}

	assert.True(t, b, "Couldnt get all channels")
}

func TestStartStreamingToListeners(t *testing.T) {
	sm := createTestStatsManager()

	chListener := make(chan StatusUpdate, 10)
	count := 5 //FOR THIS CASE: count < cap(ch)
	var arrIn []StatusUpdate

	for i := 0; i <= count; i++ {
		su := &StatusUpdate{Status: StatusSuccess}
		sm.Enqueue(su)
		arrIn = append(arrIn, *su)
	}

	sm.AddListener("1", chListener)
	go sm.StartStreamingToListeners()

	arrOut := make([]StatusUpdate, 6)

	for i := 0; i <= count; i++ {
		data := <-chListener
		arrOut[i] = data
	}

	b := true

	assert.Equal(t, len(arrIn), len(arrOut), "Arrays are not of equal length")

	for i := 0; i <= count; i++ {
		if arrIn[i] != arrOut[i] {
			b = false
		}
	}

	assert.True(t, b, "Input and Output are not the same")
}

func TestStartStreamingToListeners_BufferFull(t *testing.T) {
	sm := createTestStatsManager()

	chListener := make(chan StatusUpdate, 10)
	count := 20
	var arrIn []StatusUpdate

	for i := 0; i <= count; i++ {
		su := &StatusUpdate{Status: StatusSuccess}
		sm.Enqueue(su)
		arrIn = append(arrIn, *su)
	}

	sm.AddListener("1", chListener)

	go sm.StartStreamingToListeners()

	time.Sleep(5 * time.Second) //Send all requests before starting to process them

	ch1 := make(chan int)

	go func() {
		arrOut := make([]StatusUpdate, 11)

		for i := 0; i <= count; i++ {
			data := <-chListener
			arrOut[i] = data
		}

		b := true

		assert.Equal(t, len(arrIn), len(arrOut), "Arrays are not of equal length")

		for i := 0; i <= count; i++ {
			if arrIn[i] != arrOut[i] {
				b = false
			}
		}

		assert.True(t, b, "Input and Output are not the same")

		ch1 <- 1
	}()

	select {
	case <-ch1:
	case <-time.After(10 * time.Second):
		t.Errorf("ERROR: Channel blocked instead of dropping events")
		pid := os.Getpid()
		process, _ := os.FindProcess(pid)
		process.Signal(syscall.SIGTERM)
	}

}

func TestStartStreamingToListeners_Concurrent(t *testing.T) {
	t.Logf("Dont worry this test takes about 100 seconds")

	sm := createTestStatsManager()

	chListener := make(chan StatusUpdate, 10000) //use same buffer we defined in main
	resChanExpect := make(chan []StatusUpdate, 1)

	sm.AddListener("1", chListener)

	go sm.StartStreamingToListeners()

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func(resChan chan []StatusUpdate, wg *sync.WaitGroup) {

		count := 200
		var arrIn []StatusUpdate

		for i := 0; i <= count; i++ {
			su := &StatusUpdate{Status: StatusSuccess}
			sm.Enqueue(su)
			arrIn = append(arrIn, *su)
			time.Sleep(500 * time.Millisecond)
		}

		resChan <- arrIn
		wg.Done()
	}(resChanExpect, &wg)

	resChanActual := make(chan []StatusUpdate, 1)

	wg.Add(1)
	go func(resChan chan []StatusUpdate, wg *sync.WaitGroup) {

		count := 200
		var arrOut []StatusUpdate

		for i := 0; i <= count; i++ {
			data := <-chListener
			arrOut = append(arrOut, data)
			time.Sleep(500 * time.Millisecond)
		}

		resChan <- arrOut
		wg.Done()
	}(resChanActual, &wg)

	wg.Wait()

	expected := <-resChanExpect
	actual := <-resChanActual

	b := true

	if len(expected) == len(actual) {
		for i := range expected {
			if expected[i] != actual[i] {
				b = false
			}
		}
	} else {
		b = false
	}

	assert.True(t, b, "Expected and equal are not equal")

}
