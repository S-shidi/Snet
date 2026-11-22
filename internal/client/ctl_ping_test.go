package client

import "testing"

func TestParsePingOutput(t *testing.T) {
	cases := []struct {
		name string
		out  string
		loss float64
		avg  float64
	}{
		{
			name: "linux",
			out:  "3 packets transmitted, 3 received, 0% packet loss, time 2003ms\nrtt min/avg/max/mdev = 10.100/12.500/15.000/1.000 ms\n",
			loss: 0,
			avg:  12.5,
		},
		{
			name: "mac",
			out:  "3 packets transmitted, 2 packets received, 33.3% packet loss\nround-trip min/avg/max/stddev = 8.000/9.500/11.000/1.000 ms\n",
			loss: 33.3,
			avg:  9.5,
		},
		{
			name: "windows",
			out:  "\nPinging 10.88.0.2 with 32 bytes of data:\nReply from 10.88.0.2: bytes=32 time=12ms TTL=64\n\nPing statistics for 10.88.0.2:\n    Packets: Sent = 3, Received = 3, Lost = 0 (0% loss),\nApproximate round trip times in milli-seconds:\n    Minimum = 8ms, Maximum = 15ms, Average = 12ms\n",
			loss: 0,
			avg:  12,
		},
		{
			name: "unreachable",
			out:  "3 packets transmitted, 0 packets received, 100% packet loss\n",
			loss: 100,
			avg:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loss, avg := parsePingOutput(tc.out)
			if loss != tc.loss || avg != tc.avg {
				t.Fatalf("got loss=%v avg=%v, want loss=%v avg=%v", loss, avg, tc.loss, tc.avg)
			}
		})
	}
}
