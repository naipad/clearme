package crypt

import "testing"

func TestSHA512CryptVector(t *testing.T) {
	// 来自 sha-crypt 规范的测试向量。
	got := SHA512Crypt("Hello world!", "saltstring")
	want := "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestSHA512CryptLongPassword(t *testing.T) {
	// 来自 sha-crypt 规范的测试向量。
	got := SHA512Crypt("This is just a test", "toolongsaltstring")
	want := "$6$toolongsaltstrin$lQ8jolhgVRVhY4b5pZKaysCLi0QBxGoNeKQzQ3glMhwllF7oGDZxUhx1yxdYcz/e1JSbq3y6JMxxl8audkUEm0"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
