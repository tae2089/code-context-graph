/** @intent 안전한 정수 나눗셈 */
[[nodiscard]]
[[deprecated("use divide_safe")]]
inline int divide(int a, int b) {
    return a / b;
}
