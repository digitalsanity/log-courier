/*
 * Copyright 2014 Jason Woods.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package codecs

import (
  "errors"
  "fmt"
  "lc-lib/core"
  "regexp"
  "strings"
  "sync"
  "time"
)

const (
  codecMultiline_What_Previous = 0x00000001
  codecMultiline_What_Next     = 0x00000002
)

type CodecMultilineFactory struct {
  Pattern         string        `config:"pattern"`
  What            string        `config:"what"`
  Negate          bool          `config:"negate"`
  PreviousTimeout time.Duration `config:"previous timeout"`

  matcher *regexp.Regexp
  what    int
}

type CodecMultiline struct {
  config        *CodecMultilineFactory
  last_offset   int64
  callback_func core.CodecCallbackFunc

  end_offset     int64
  start_offset   int64
  line           uint64
  buffer         []string
  buffer_lines   uint64
  timer_lock     sync.Mutex
  timer_stop     chan interface{}
  timer_wait     sync.WaitGroup
  timer_deadline time.Time
}

func NewMultilineCodecFactory(config *core.Config, config_path string, unused map[string]interface{}, name string) (core.CodecFactory, error) {
  var err error

  result := &CodecMultilineFactory{}
  if err = config.PopulateConfig(result, config_path, unused); err != nil {
    return nil, err
  }

  if result.Pattern == "" {
    return nil, errors.New("Multiline codec pattern must be specified.")
  }

  result.matcher, err = regexp.Compile(result.Pattern)
  if err != nil {
    return nil, fmt.Errorf("Failed to compile multiline codec pattern, '%s'.", err)
  }

  if result.What == "" || result.What == "previous" {
    result.what = codecMultiline_What_Previous
  } else if result.What == "next" {
    result.what = codecMultiline_What_Next
  }

  return result, nil
}

func (f *CodecMultilineFactory) NewCodec(callback_func core.CodecCallbackFunc, offset int64) core.Codec {
  c := &CodecMultiline{
    config:        f,
    end_offset:    offset,
    last_offset:   offset,
    callback_func: callback_func,
  }

  // Start the "previous timeout" routine that will auto flush at deadline
  if f.PreviousTimeout != 0 {
    c.timer_stop = make(chan interface{})
    c.timer_wait.Add(1)

    c.timer_deadline = time.Now().Add(f.PreviousTimeout)

    go c.deadlineRoutine()
  }
  return c
}

func (c *CodecMultiline) Teardown() int64 {
  if c.config.PreviousTimeout != 0 {
    close(c.timer_stop)
    c.timer_wait.Wait()
  }

  return c.last_offset
}

func (c *CodecMultiline) Event(start_offset int64, end_offset int64, line uint64, text string) {
  // TODO(driskell): If we are using previous and we match on the very first line read,
  // then this is because we've started in the middle of a multiline event (the first line
  // should never match) - so we could potentially offer an option to discard this.
  // The benefit would be that when using previous_timeout, we could discard any extraneous
  // event data that did not get written in time, if the user so wants it, in order to prevent
  // odd incomplete data. It would be a signal from the user, "I will worry about the buffering
  // issues my programs may have - you just make sure to write each event either completely or
  // partially, always with the FIRST line correct (which could be the important one)."
  match_failed := c.config.Negate == c.config.matcher.MatchString(text)
  if c.config.what == codecMultiline_What_Previous {
    if c.config.PreviousTimeout != 0 {
      // Prevent a flush happening while we're modifying the stored data
      c.timer_lock.Lock()
    }
    if match_failed {
      c.flush()
    }
  }
  if len(c.buffer) == 0 {
    c.line = line
    c.start_offset = start_offset
  }
  c.end_offset = end_offset
  c.buffer = append(c.buffer, text)
  c.buffer_lines++
  if c.config.what == codecMultiline_What_Previous {
    if c.config.PreviousTimeout != 0 {
      // Reset the timer and unlock
      c.timer_deadline = time.Now().Add(c.config.PreviousTimeout)
      c.timer_lock.Unlock()
    }
  } else if c.config.what == codecMultiline_What_Next && match_failed {
    c.flush()
  }
}

func (c *CodecMultiline) flush() {
  if len(c.buffer) == 0 {
    return
  }

  text := strings.Join(c.buffer, "\n")

  // Set last offset - this is returned in Teardown so if we're mid multiline and crash, we start this multiline again
  c.last_offset = c.end_offset
  c.buffer = nil
  c.buffer_lines = 0

  c.callback_func(c.start_offset, c.end_offset, c.line, text)
}

func (c *CodecMultiline) Snapshot() *core.Snapshot {
  snap := core.NewSnapshot("Multiline Codec")
  snap.AddEntry("Pending lines", c.buffer_lines)
  snap.AddEntry("Pending bytes", c.last_offset - c.end_offset)
  return snap
}

func (c *CodecMultiline) deadlineRoutine() {
  timer := time.NewTimer(0)

DeadlineLoop:
  for {
    select {
    case <-c.timer_stop:
      timer.Stop()

      // Shutdown signal so end the routine
      break DeadlineLoop
    case now := <-timer.C:
      c.timer_lock.Lock()

      // Have we reached the target time?
      if !now.After(c.timer_deadline) {
        // Deadline moved, update the timer
        timer.Reset(c.timer_deadline.Sub(now))
      }

      c.flush()

      c.timer_lock.Unlock()
    }
  }

  c.timer_wait.Done()
}

// Register the codec
func init() {
  core.RegisterCodec("multiline", NewMultilineCodecFactory)
}