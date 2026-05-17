package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Job struct {
	ID int
}

func producer(ctx context.Context, id int, jobs chan<- Job, wg *sync.WaitGroup) {
	defer wg.Done()

	for i := 0; ; i++ {
		job := Job{ID: id*1000 + i}

		select {
		case jobs <- job:
			fmt.Printf("producer %d produced job %d\n", id, job.ID)
		case <-ctx.Done():
			fmt.Printf("producer %d exit: %v\n", id, ctx.Err())
			return
		}

		time.Sleep(100 * time.Millisecond)
	}
}

func consumer(ctx context.Context, id int, jobs <-chan Job, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		select {
		case job, ok := <-jobs:
			if !ok {
				return
			}
			fmt.Printf("consumer %d handled job %d\n", id, job.ID)
		case <-ctx.Done():
			fmt.Printf("consumer %d exit: %v\n", id, ctx.Err())
			return
		}
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	jobs := make(chan Job, 100)

	var producerWG sync.WaitGroup
	var consumerWG sync.WaitGroup

	for i := 0; i < 5; i++ {
		consumerWG.Add(1)
		go consumer(ctx, i, jobs, &consumerWG)
	}

	for i := 0; i < 3; i++ {
		producerWG.Add(1)
		go producer(ctx, i, jobs, &producerWG)
	}

	producerWG.Wait()
	close(jobs)
	consumerWG.Wait()
}
