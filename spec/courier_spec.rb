# Copyright 2014 Jason Woods.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

require 'lib/common'
require 'lib/helpers/log-courier'

describe 'log-courier' do
  include_context 'Helpers'
  include_context 'Helpers_Log_Courier'

  it 'should follow stdin' do
    startup mode: 'w', config: <<-config
    {
      "network": {
        "ssl ca": "#{@ssl_cert.path}",
        "servers": [ "127.0.0.1:#{server_port}" ],
        "timeout": 15,
        "reconnect": 1
      },
      "files": [
        {
          "paths": [ "-" ]
        }
      ]
    }
    config

    5_000.times do |i|
      @log_courier.puts "stdin line test #{i}"
    end

    # Receive and check
    i = 0
    host = Socket.gethostname
    receive_and_check(total: 5_000) do |e|
      expect(e['message']).to eq "stdin line test #{i}"
      expect(e['host']).to eq host
      expect(e['file']).to eq '-'
      i += 1
    end
  end

  it 'should follow a file from the end' do
    # Hide lines in the file - this makes sure we start at the end of the file
    f = create_log.log(50).skip

    startup

    f.log 5_000

    # Receive and check
    receive_and_check
  end

  it 'should follow a file from the beginning with parameter -from-beginning=true' do
    # Hide lines in the file - this makes sure we start at the beginning of the file
    f = create_log.log(50)

    startup args: '-from-beginning=true'

    f.log 5000

    # Receive and check
    receive_and_check
  end

  it 'should follow a slowly-updating file' do
    startup

    f = create_log

    100.times do |i|
      f.log 50

      # Start fast, then go slower after 80% of the events
      # Total sleep becomes 20 seconds
      sleep 1 if i > 80
    end

    # Quickly test we received at least 90% already
    # If not, then the 5 second idle_timeout has been ignored and test fails
    expect(@event_queue.length).to be >= 4_500

    # Receive and check
    receive_and_check
  end

  it 'should follow multiple files and resume them when restarted' do
    f1 = create_log
    f2 = create_log

    startup

    5000.times do
      f1.log
      f2.log
    end

    # Receive and check
    receive_and_check

    # Now restart logstash
    shutdown

    # From beginning makes testing this easier - without it we'd need to create lines inbetween shutdown and start and verify them which is more work
    startup args: '-from-beginning=true'

    f1 = create_log
    f2 = create_log
    5_000.times do
      f1.log
      f2.log
    end

    # Receive and check
    receive_and_check
  end

  it 'should start newly created files found after startup from beginning and not the end' do
    # Create a file and hide it
    f1 = create_log.log(5_000)
    path = f1.path
    f1.rename File.join(File.dirname(path), 'hide-' + File.basename(path))

    startup

    create_log.log 5_000

    # Throw the file back with all the content already there
    # We can't just create a new one, it might pick it up before we write
    f1.rename path

    # Receive and check
    receive_and_check
  end

  it 'should handle incomplete lines in buffered logs by waiting for a line end' do
    f = create_log

    startup

    1_000.times do |i|
      if (i + 100) % 500 == 0
        # Make 2 events where we pause for >10s before adding new line, this takes us past eof_timeout
        f.log_partial_start
        sleep 15
        f.log_partial_end
      else
        f.log
      end
    end

    # Receive and check
    receive_and_check
  end

  it 'should handle log rotation and resume correctly' do
    f1 = create_log

    startup

    f1.log 100

    # Receive and check
    receive_and_check

    # Rotate f1 - this renames it and returns a new file same name as original f1
    f2 = rotate(f1)

    # Write to both
    f1.log 5_000
    f2.log 5_000

    # Receive and check
    receive_and_check

    # Restart
    shutdown
    startup

    # Write some more
    f1.log 5_000
    f2.log 5_000

    # Receive and check - but not file as it will be different now
    receive_and_check check_file: false
  end

  it 'should handle log rotation and resume correctly even if rotated file moves out of scope' do
    startup

    f1 = create_log.log 100

    # Receive and check
    receive_and_check

    # Rotate f1 - this renames it and returns a new file same name as original f1
    # But prefix it so it moves out of scope
    f2 = rotate(f1, 'r')

    # Write to both - but a bit more to the out of scope
    f1.log 5_000
    f2.log 5_000
    f1.log 5_000

    # Receive and check
    receive_and_check

    # Restart
    shutdown
    startup

    # Write some more but remember f1 should be out of scope
    f1.log(5000).skip 5000
    f2.log 5000

    # Receive and check - but not file as it will be different now
    receive_and_check check_file: false
  end

  it 'should handle log rotation and resume correctly even if rotated file updated' do
    startup

    f1 = create_log.log 100

    # Receive and check
    receive_and_check

    # Rotate f1 - this renames it and returns a new file same name as original f1
    f2 = rotate(f1)

    # Write to both
    f1.log 5_000
    f2.log 5_000

    # Make the last update go to f1 (the rotated file)
    # This can throw up an edge case we used to fail
    sleep 10
    f1.log 5_000

    # Receive and check
    receive_and_check

    # Restart
    shutdown
    startup

    # Write some more
    f1.log 5_000
    f2.log 5_000

    # Receive and check - but not file as it will be different now
    receive_and_check check_file: false
  end

  it 'should handle log rotation during startup resume' do
    startup

    f1 = create_log.log 100

    # Receive and check
    receive_and_check

    # Stop
    shutdown

    # Rotate f1 - this renames it and returns a new file same name as original f1
    f2 = rotate(f1)

    # Write to both
    f1.log 5_000
    f2.log(5_000).skip 5_000

    # Start again
    startup

    # Receive and check - but not file as it will be different now
    receive_and_check check_file: false
  end

  it 'should resume harvesting a file that reached dead time but changed again' do
    startup config: <<-config
    {
      "network": {
        "ssl ca": "#{@ssl_cert.path}",
        "servers": [ "127.0.0.1:#{server_port}" ],
        "timeout": 15,
        "reconnect": 1
      },
      "files": [
        {
          "paths": [ "#{TEMP_PATH}/logs/log-*" ],
          "dead time": "5s"
        }
      ]
    }
    config

    f1 = create_log.log(5_000)

    # Receive and check
    receive_and_check

    # Let dead time occur
    sleep 15

    # Write again
    f1.log(5_000)

    # Receive and check
    receive_and_check
  end

  it 'should prune deleted files from registrar state' do
    # We use dead time to make sure the harvester stops, as file deletion is only acted upon once the harvester stops
    startup config: <<-config
    {
      "network": {
        "ssl ca": "#{@ssl_cert.path}",
        "servers": [ "127.0.0.1:#{server_port}" ],
        "timeout": 15,
        "reconnect": 1
      },
      "files": [
        {
          "paths": [ "#{TEMP_PATH}/logs/log-*" ],
          "dead time": "5s"
        }
      ]
    }
    config

    # Write lines
    f1 = create_log.log(5_000)
    create_log.log 5_000

    # Receive and check
    receive_and_check

    # Grab size of the saved state - sleep to ensure it was saved
    sleep 1
    s = File::Stat.new('.log-courier').size

    # Close and delete one of the files
    f1.close

    # Wait for prospector to realise it is deleted
    sleep 15

    # Check new size of registrar state
    expect(File::Stat.new('.log-courier').size).to be < s
  end

  it 'should allow use of a custom persist directory' do
    f = create_log

    config = <<-config
    {
      "general": {
        "persist directory": "#{TEMP_PATH}"
      },
      "network": {
        "ssl ca": "#{@ssl_cert.path}",
        "servers": [ "127.0.0.1:#{server_port}" ]
      },
      "files": [
        {
          "paths": [ "#{TEMP_PATH}/logs/log-*" ]
        }
      ]
    }
    config

    startup config: config

    # Write logs
    f.log 5_000

    # Receive and check
    receive_and_check

    # Restart - use from-beginning so we fail if we don't resume
    shutdown
    startup config: config, args: '-from-beginning=true'

    # Write some more
    f.log 5_000

    # Receive and check
    receive_and_check

    # We have to clean up ourselves here since .log-courer is elsewhere
    # Do some checks to ensure we used a different location though
    shutdown
    expect(File.file?(".log-courier")).to be false
    expect(File.file?("#{TEMP_PATH}/.log-courier")).to be true
    File.unlink("#{TEMP_PATH}/.log-courier")
  end
end
