#!/usr/bin/env bash
# Run while the disposable emulator is alive, retaining screenshots even if a UI assertion fails.
set -uo pipefail
result=0
./android/gradlew -p android --no-daemon -PstoreTestBuildType=e2e -Pandroid.testInstrumentationRunnerArguments.class=dev.jeeth.privatestore.EdgeNavigationTest connectedE2eAndroidTest || result=$?
mkdir -p android/app/build/reports/ui/edge-navigation
cp -a android/app/build/reports/androidTests/connected/. android/app/build/reports/ui/edge-navigation/ 2>/dev/null || true
./android/gradlew -p android --no-daemon -PstoreTestBuildType=e2e -Pandroid.testInstrumentationRunnerArguments.class=dev.jeeth.privatestore.NotificationRelayPowerTest,dev.jeeth.privatestore.NotificationRelayRecoveryTest,dev.jeeth.privatestore.StoreCatalogTest,dev.jeeth.privatestore.InstallCacheTest,dev.jeeth.privatestore.BrokerDeviceTest connectedE2eAndroidTest || result=$?
mkdir -p android/app/build/reports/ui
for name in catalog-grid app-details parallel-progress settings; do
    adb exec-out run-as dev.jeeth.privatestore.testbed cat "files/$name.png" > "android/app/build/reports/ui/$name.png" || rm -f "android/app/build/reports/ui/$name.png"
done
adb shell dumpsys window > android/app/build/reports/ui/navigation-windows.txt
adb shell dumpsys activity service com.android.systemui/.SystemUIService > android/app/build/reports/ui/navigation-systemui.txt
adb logcat -d -s WindowOnBackInvokedDispatcher EdgeBackGestureHandler BackAnimationController InputMethodManager > android/app/build/reports/ui/navigation-logcat.txt
exit "$result"
