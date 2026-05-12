#!/bin/bash

# Ensure the 'ghwebhook' session exists
if ! tmux has-session -t ghwebhook 2>/dev/null; then
  # Create a new session named 'ghwebhook', detached
  tmux new-session -d -s ghwebhook
  
  # Split the window horizontally (-h)
  # The left pane will remain a terminal
  # The right pane will run 'gh dash'
  tmux split-window -h -t ghwebhook "gh dash"
fi
