#!/bin/bash
# Requires vegeta to be installed
# brew install vegeta

echo "GET http://localhost:8080/getLyrics?s=Comfortably+Numb&a=Pink+Floyd" | vegeta attack -duration=10s -rate=100 | vegeta report
