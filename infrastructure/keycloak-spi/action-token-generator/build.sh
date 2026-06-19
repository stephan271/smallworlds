#!/bin/bash
set -e

cd "$(dirname "$0")"
mvn clean package
echo "Build complete. JAR is located in target/action-token-generator-1.0.0-SNAPSHOT.jar"
