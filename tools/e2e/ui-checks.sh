#!/usr/bin/env bash
# Run while the disposable emulator is alive, retaining screenshots even if a UI assertion fails.
set -uo pipefail
./android/gradlew -p android --no-daemon -PstoreTestBuildType=e2e -Pandroid.testInstrumentationRunnerArguments.class=dev.jeeth.privatestore.NotificationRelayPowerTest,dev.jeeth.privatestore.StoreCatalogTest,dev.jeeth.privatestore.InstallCacheTest connectedE2eAndroidTest
result=$?
mkdir -p android/app/build/reports/ui
for name in catalog-grid app-details parallel-progress settings; do
    adb exec-out run-as dev.jeeth.privatestore.testbed cat "files/$name.png" > "android/app/build/reports/ui/$name.png" || rm -f "android/app/build/reports/ui/$name.png"
done
exit "$result"
