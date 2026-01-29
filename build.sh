#!/bin/bash
set -e

echo "Starting build process..."

rm -rf build
rm -rf http/ui/dist

cd http/ui
pnpm install
pnpm run build

cd ../../

go build -o ./build/
chmod +x ./build/*
echo "Build completed successfully."
