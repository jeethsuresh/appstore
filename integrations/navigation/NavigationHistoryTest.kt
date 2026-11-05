package cool.gravitas.navigation

import org.junit.Assert.*
import org.junit.Test

class NavigationHistoryTest {
    @Test fun chronologicalHistoryAndNewBranch() {
        val history = NavigationHistory("home")
        assertNull(history.back()); assertNull(history.forward())
        history.visit("list"); history.visit("details")
        assertEquals("list", history.back()); assertEquals("home", history.back())
        assertEquals("list", history.forward()); assertEquals("details", history.forward())
        history.back(); history.visit("another item")
        assertNull(history.forward())
    }
    @Test fun resetCannotReturnToAnotherAccountAndHistoryIsBounded() {
        val history = NavigationHistory("home", 2)
        history.visit("one"); history.visit("two"); history.visit("three")
        assertEquals("two", history.back()); assertEquals("one", history.back()); assertNull(history.back())
        history.reset("new account")
        assertNull(history.back()); assertNull(history.forward())
    }
}
