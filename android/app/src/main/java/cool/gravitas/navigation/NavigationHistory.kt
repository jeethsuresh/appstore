package cool.gravitas.navigation

/** Memory-only navigation, never an action replay log. */
class NavigationHistory<T>(initial: T, private val limit: Int = 64) {
    private val past = ArrayList<T>()
    private val future = ArrayList<T>()
    var current: T = initial
        private set
    val canBack get() = past.isNotEmpty()
    val canForward get() = future.isNotEmpty()
    fun visit(next: T) {
        if (next == current) return
        past.add(current)
        if (past.size > limit) past.removeAt(0)
        current = next
        future.clear()
    }
    fun back(): T? {
        if (!canBack) return null
        future.add(current)
        current = past.removeAt(past.lastIndex)
        return current
    }
    fun forward(): T? {
        if (!canForward) return null
        past.add(current)
        current = future.removeAt(future.lastIndex)
        return current
    }
    fun reset(next: T) { past.clear(); future.clear(); current = next }
}
