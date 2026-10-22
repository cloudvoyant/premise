import json
import os
import subprocess

with open(os.environ["APP_CONFIG"]) as source:
    secrets = json.load(source)
args = ["pm", "infra", "query", "--env", os.environ["PM_ENV"], "--output", "bucketName"]
if os.environ["PM_ENV"] == "preview":
    args += ["--branch", os.environ["PM_BRANCH"]]
bucket = subprocess.check_output(args, text=True).strip()
print("APP_MESSAGE=" + json.dumps(secrets["message"]))
print("BUCKET_NAME=" + json.dumps(bucket))
