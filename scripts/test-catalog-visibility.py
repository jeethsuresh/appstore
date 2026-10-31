#!/usr/bin/env python3
import unittest
import catalog_visibility as visibility


class CatalogVisibilityTest(unittest.TestCase):
    def test_only_the_store_itself_is_automatically_visible(self):
        self.assertEqual({"cool.gravitas.atlas"}, visibility.required_packages([
            {"packageName": "dev.jeeth.privatestore"}, {"packageName": "cool.gravitas.atlas"}]))

    def test_desktop_only_apps_need_no_android_visibility(self):
        self.assertEqual(set(), visibility.required_packages([{"id": "office", "desktop": True}]))

    def test_catalog_addition_requires_a_visibility_declaration(self):
        visible = visibility.source_packages('''<manifest xmlns:android="http://schemas.android.com/apk/res/android">
            <queries><package android:name="cool.gravitas.river"/></queries>
            <application android:name="cool.gravitas.atlas"/>
            </manifest>''')
        visibility.require_visible({"cool.gravitas.river"}, visible)
        with self.assertRaisesRegex(ValueError, "cool.gravitas.atlas"):
            visibility.require_visible({"cool.gravitas.river", "cool.gravitas.atlas"}, visible)

    def test_compiled_manifest_requires_packages_inside_queries(self):
        manifest = '''N: android=http://schemas.android.com/apk/res/android (line=1)
  E: manifest (line=1)
    A: package="dev.jeeth.privatestore" (Raw: "dev.jeeth.privatestore")
    E: uses-permission (line=2)
      A: android:name(0x01010003)="cool.gravitas.atlas" (Raw: "cool.gravitas.atlas")
    E: queries (line=3)
      E: package (line=4)
        A: android:name(0x01010003)="cool.gravitas.river" (Raw: "cool.gravitas.river")
      E: package (line=5)
        A: android:name(0x01010003)="cool.gravitas.atlas" (Raw: "cool.gravitas.atlas")
    E: application (line=6)
      A: android:name(0x01010003)="not.a.query" (Raw: "not.a.query")
      E: package (line=7)
        A: android:name(0x01010003)="also.not.visible" (Raw: "also.not.visible")
'''
        self.assertEqual({"cool.gravitas.river", "cool.gravitas.atlas"}, visibility.compiled_packages(manifest))
        # The pinned aapt2 emits the expanded namespace URI, while aapt uses the prefix.
        expanded = manifest.replace("A: android:", "A: http://schemas.android.com/apk/res/android:")
        self.assertEqual({"cool.gravitas.river", "cool.gravitas.atlas"}, visibility.compiled_packages(expanded))
        # A catalog-only change used to pass PR CI and leave Atlas invisible on Android.
        without_atlas = manifest.replace('      E: package (line=5)\n        A: android:name(0x01010003)="cool.gravitas.atlas" (Raw: "cool.gravitas.atlas")\n', '')
        with self.assertRaisesRegex(ValueError, "cool.gravitas.atlas"):
            visibility.require_visible({"cool.gravitas.atlas"}, visibility.compiled_packages(without_atlas))


if __name__ == "__main__":
    unittest.main()
