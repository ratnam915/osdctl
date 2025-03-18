#!/bin/bash
set -e
HOOK_DIR=".git/hooks"
SCRIPTS_DIR="scripts/"
mkdir -p $HOOK_DIR
mkdir -p $SCRIPTS_DIR
cat > "$SCRIPTS_DIR/pre-commit" << 'EOL'
#!/bin/bash
set -e
echo "Running pre-commit hook to check docs/ directory..."
CHANGED_FILES=$(git diff --staged --name-only)
if echo "$CHANGED_FILES" | grep -q -v "^docs/" && echo "$CHANGED_FILES" | grep -q "\.go$"; then
 echo "Go files changed, checking if docs need updating..."
 if ! make generate-docs; then
 echo "Failed to generate documentation. Fix errors before committing."
 exit 1
 fi
 if git diff --name-only | grep -q "^docs/"; then
 echo "ERROR: Documentation in docs/ directory is out of date."
 echo "Please stage the updated documentation before committing:"
 echo " git add docs/"
 exit 1
 fi
fi
echo "Pre-commit hook completed successfully."
exit 0
EOL
chmod +x "$SCRIPTS_DIR/pre-commit"
ln -sf "../../$SCRIPTS_DIR/pre-commit" "$HOOK_DIR/pre-commit"
echo "Git hook installed successfully!"
echo "Pre-commit hook will check for changes in the docs/ directory when source files are modified."