#!/bin/bash
# Development Environment Setup Script for vb-cli
# This script sets up all necessary tools and hooks for contributing

set -e

echo "🚀 Setting up vb-cli development environment..."

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

# Check if Go is installed
if ! command -v go &> /dev/null; then
    print_error "Go is not installed. Please install Go 1.25 or later."
    echo "Visit: https://golang.org/doc/install"
    exit 1
fi

# Check Go version
GO_VERSION=$(go version | cut -d' ' -f3 | sed 's/go//')
REQUIRED_VERSION="1.25"
if ! printf '%s\n' "$REQUIRED_VERSION" "$GO_VERSION" | sort -V -C; then
    print_error "Go version $GO_VERSION is too old. Please upgrade to Go $REQUIRED_VERSION or later."
    exit 1
fi
print_status "Go version $GO_VERSION is compatible"

# Install pre-commit if not available
if ! command -v pre-commit &> /dev/null; then
    print_warning "pre-commit not found. Installing..."
    if command -v pip3 &> /dev/null; then
        pip3 install pre-commit
    elif command -v pip &> /dev/null; then
        pip install pre-commit
    elif command -v brew &> /dev/null; then
        brew install pre-commit
    else
        print_error "Could not install pre-commit. Please install manually:"
        echo "  pip install pre-commit"
        echo "  OR"
        echo "  brew install pre-commit"
        exit 1
    fi
fi
print_status "pre-commit is available"

# Install pre-commit hooks
print_status "Installing pre-commit hooks..."
pre-commit install
pre-commit install --hook-type commit-msg

# Download Go dependencies
print_status "Downloading Go dependencies..."
go mod download
go mod verify

# Run initial tests to ensure everything works
print_status "Running initial tests..."
if make test; then
    print_status "All tests pass!"
else
    print_error "Tests failed. Please check the output above."
    exit 1
fi

# Run security scan
print_status "Running security scan..."
if make scan; then
    print_status "Security scan passed!"
else
    print_error "Security scan failed. Please check the output above."
    exit 1
fi

# Build the project
print_status "Building project..."
if make build; then
    print_status "Build successful!"
else
    print_error "Build failed. Please check the output above."
    exit 1
fi

# Create the binary
print_status "Creating binary..."
if make package; then
    print_status "Binary created at dist/vb"
else
    print_error "Binary creation failed. Please check the output above."
    exit 1
fi

# Test the binary
print_status "Testing binary..."
if ./dist/vb version; then
  print_status "Binary works correctly!"
else
  print_error "Binary test failed."
  exit 1
fi

# Run pre-commit on all files to ensure everything is clean
print_status "Running pre-commit checks on all files..."
if pre-commit run --all-files; then
    print_status "All pre-commit checks passed!"
else
    print_warning "Some pre-commit checks failed. This is normal for initial setup."
    print_warning "You may need to run 'pre-commit run --all-files' again after fixing issues."
fi

echo ""
echo "🎉 Development environment setup complete!"
echo ""
echo "📋 Quick reference:"
echo "  make test      - Run tests with coverage"
echo "  make scan      - Run security scan"
echo "  make build     - Build all packages"
echo "  make package   - Create binary"
echo "  make pre-commit - Run all pre-commit checks"
echo ""
echo "🔧 Development workflow:"
echo "  1. Make your changes"
echo "  2. Run 'make test scan' to verify"
echo "  3. Commit (pre-commit hooks will run automatically)"
echo "  4. Push and create PR"
echo ""
echo "📚 Documentation:"
echo "  docs/DEVELOPMENT.md - Development guide"
echo "  docs/CLI.md        - CLI reference"
echo "  README.md          - Project overview"
echo ""
print_status "Happy coding! 🚀"
