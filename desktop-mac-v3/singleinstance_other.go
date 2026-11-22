//go:build !darwin

package main

func acquireSingleInstance() (func(), bool) { return func() {}, true }
