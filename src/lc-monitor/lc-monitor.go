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

package main

import (
  "bufio"
  "flag"
  "fmt"
  "lc-lib/admin"
  "lc-lib/core"
  "os"
  "os/signal"
  "strings"
)



type Admin struct {
  client    *admin.Client
  connected bool
  quiet     bool
  host      string
  port      int
}

func NewAdmin(quiet bool, host string, port int) *Admin {
  return &Admin{
    quiet: quiet,
    host:  host,
    port:  port,
  }
}

func (a *Admin) ensureConnected() error {
  if !a.connected {
    var err error

    if !a.quiet {
      fmt.Printf("Attempting connection to %s:%d...\n", a.host, a.port)
    }

    if a.client, err = admin.NewClient(a.host, a.port); err != nil {
      fmt.Printf("Failed to connect: %s\n", err)
      return err
    }

    if !a.quiet {
      fmt.Printf("Connected\n\n")
    }

    a.connected = true
  }

  return nil
}

func (a *Admin) ProcessCommand(command string) bool {
  if err := a.ensureConnected(); err != nil {
    return false
  }

  var err error

  switch command {
  case "ping":
    err = a.client.Ping()
    if err != nil {
      break
    }

    fmt.Printf("Pong\n")
  case "snapshot":
    var snapshots []*core.Snapshot

    snapshots, err = a.client.FetchSnapshot()
    if err != nil {
      break
    }

    for _, snap := range snapshots {
      fmt.Printf("%s:\n", snap.Description())
      for i, j := 0, snap.NumEntries(); i < j; i = i+1 {
        k, v := snap.Entry(i)
        fmt.Printf("  %s = %v\n", k, v)
      }
    }
  default:
    fmt.Printf("Unknown command: %s\n", command)
  }

  if err == nil {
    return true
  }

  if _, ok := err.(*admin.ErrorResponse); ok {
    fmt.Printf("Log Courier returned an error: %s\n", err)
  } else {
    a.connected = false
    fmt.Printf("Connection error: %s\n", err)
  }

  return false
}

func (a *Admin) Run() {
  signal_chan := make(chan os.Signal, 1)
  signal.Notify(signal_chan, os.Interrupt)

  command_chan := make(chan string)
  go func() {
    var discard bool
    reader := bufio.NewReader(os.Stdin)
    for {
      line, prefix, err := reader.ReadLine()
      if err != nil {
        break
      } else if prefix {
        discard = true
      } else if discard {
        fmt.Printf("Line too long!\n")
        discard = false
      } else {
        command_chan <- string(line)
      }
    }
  }()

CommandLoop:
  for {
    fmt.Printf("> ")
    select {
    case command := <-command_chan:
      a.ProcessCommand(command)
    case <-signal_chan:
      fmt.Printf("\n> exit\n")
      break CommandLoop
    }
  }
}

func main() {
  var version bool
  var quiet bool
  var host string
  var port int

  flag.BoolVar(&version, "version", false, "display the Log Courier client version")
  flag.BoolVar(&quiet, "quiet", false, "quietly execute the command line argument and output only the result")
  flag.StringVar(&host, "host", "127.0.0.1", "the Log Courier host to connect to (default 127.0.0.1)")
  flag.IntVar(&port, "port", 1234, "the Log Courier monitor port (default 1234)")

  flag.Parse()

  if version {
    fmt.Printf("Log Courier version %s\n", core.Log_Courier_Version)
    os.Exit(0)
  }

  if !quiet {
    fmt.Printf("Log Courier version %s client\n\n", core.Log_Courier_Version)
  }

  admin := NewAdmin(quiet, host, port)

  args := flag.Args()
  if len(args) != 0 {
    if !admin.ProcessCommand(strings.Join(args, " ")) {
      os.Exit(1)
    }
    os.Exit(0)
  }

  if quiet {
    fmt.Printf("No command specified on the command line for quiet execution\n")
    os.Exit(1)
  }

  if err := admin.ensureConnected(); err != nil {
    return
  }

  admin.Run()
}
